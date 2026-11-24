package service

import "encoding/json"

// Filtering is explicitly enabled per account. Keep the default discovery
// policy unless at least one account opts in; an ordinary or unfiltered account
// in a mixed pool can still advertise a model it serves. Never project mapping
// targets as aliases for passthrough: only source names match the wire model.
func filterOpenAIPassthroughCatalog(body []byte, accounts []Account, codex bool) ([]byte, bool, error) {
	enabled := false
	for i := range accounts {
		enabled = enabled || accounts[i].IsOpenAIPassthroughModelFilterEnabled()
	}
	if !enabled {
		return body, false, nil
	}
	field, idField := "data", "id"
	if codex {
		field, idField = "models", "slug"
	}
	envelope, entries, err := modelCatalogEntries(body, field)
	if err != nil {
		return nil, false, err
	}
	filtered := make([]json.RawMessage, 0, len(entries))
	for _, raw := range entries {
		var entry map[string]json.RawMessage
		var id string
		if json.Unmarshal(raw, &entry) != nil || json.Unmarshal(entry[idField], &id) != nil {
			continue
		}
		for i := range accounts {
			if accounts[i].Platform == PlatformOpenAI && accounts[i].IsModelSupported(id) {
				filtered = append(filtered, raw)
				break
			}
		}
	}
	if len(filtered) == len(entries) {
		return body, false, nil
	}
	envelope[field], err = json.Marshal(filtered)
	if err != nil {
		return nil, false, err
	}
	out, err := json.Marshal(envelope)
	return out, true, err
}
