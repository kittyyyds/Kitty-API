package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ModerationDataURL renders an uploaded image as a data URL for content
// moderation; non-image uploads yield "".
func (u OpenAIImagesUpload) ModerationDataURL() string {
	if len(u.Data) == 0 {
		return ""
	}
	contentType := strings.TrimSpace(u.ContentType)
	if contentType == "" {
		contentType = http.DetectContentType(u.Data)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return ""
	}
	return fmt.Sprintf("data:%s;base64,%s", contentType, base64.StdEncoding.EncodeToString(u.Data))
}

func moderationImageList(urls []string, uploads []OpenAIImagesUpload, singleURLs []string, singleUploads []*OpenAIImagesUpload) []map[string]string {
	images := make([]map[string]string, 0, len(urls)+len(uploads)+len(singleURLs)+len(singleUploads))
	for _, imageURL := range urls {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, map[string]string{"image_url": imageURL})
		}
	}
	for _, upload := range uploads {
		if dataURL := upload.ModerationDataURL(); dataURL != "" {
			images = append(images, map[string]string{"image_url": dataURL})
		}
	}
	for _, imageURL := range singleURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, map[string]string{"image_url": imageURL})
		}
	}
	for _, upload := range singleUploads {
		if upload == nil {
			continue
		}
		if dataURL := upload.ModerationDataURL(); dataURL != "" {
			images = append(images, map[string]string{"image_url": dataURL})
		}
	}
	return images
}

func marshalModerationBody(prompt string, images []map[string]string) []byte {
	payload := map[string]any{}
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		payload["prompt"] = prompt
	}
	if len(images) > 0 {
		payload["images"] = images
	}
	if len(payload) == 0 {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return body
}

// ModerationBody is the prompt and input images of an image request, in the
// shape the content-moderation extractor understands.
func (r *OpenAIImagesRequest) ModerationBody() []byte {
	if r == nil {
		return nil
	}
	return marshalModerationBody(r.Prompt, moderationImageList(
		r.InputImageURLs, r.Uploads,
		[]string{r.MaskImageURL, r.MarkupImageURL},
		[]*OpenAIImagesUpload{r.MaskUpload, r.MarkupUpload},
	))
}

// ModerationBody is the prompt and input/reference images of a Grok media request.
func (r GrokMediaRequestInfo) ModerationBody() []byte {
	return marshalModerationBody(r.Prompt, moderationImageList(
		append(append([]string(nil), r.InputImageURLs...), r.ReferenceImageURLs...), r.Uploads,
		[]string{r.MaskImageURL},
		[]*OpenAIImagesUpload{r.MaskUpload},
	))
}
