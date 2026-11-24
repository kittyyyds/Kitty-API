package service

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// A heartbeat may carry its SSE event name and exactly one data field. Keep
// these lines bounded independently of the regular first-output buffer.
type openAIPendingSSEKeepalive struct {
	lines   [2]string
	count   int
	hasName bool
	hasData bool
}

func (p *openAIPendingSSEKeepalive) addLine(line string) bool {
	if len(line) > 512 || p.count == len(p.lines) || !strings.Contains(line, "keepalive") {
		return false
	}
	if name, ok := extractOpenAISSEEventLine(line); ok && name == "keepalive" && !p.hasName {
		p.hasName = true
	} else if data, ok := extractOpenAISSEDataLine(line); ok && !p.hasData && isOpenAIKeepaliveData(data) {
		p.hasData = true
	} else {
		return false
	}
	p.lines[p.count] = line
	p.count++
	return true
}

// Accept only transport fields. Reject duplicate keys too, since clients may
// disagree about which value to use.
func isOpenAIKeepaliveData(data string) bool {
	if !gjson.Valid(data) {
		return false
	}
	payload := gjson.Parse(data)
	if !payload.IsObject() {
		return false
	}
	ok, hasType, hasSequence := true, false, false
	payload.ForEach(func(key, value gjson.Result) bool {
		switch key.String() {
		case "type":
			ok = !hasType && value.Type == gjson.String && value.String() == "keepalive"
			hasType = true
		case "sequence_number":
			_, err := strconv.ParseUint(value.Raw, 10, 64)
			ok = !hasSequence && value.Type == gjson.Number && err == nil
			hasSequence = true
		default:
			ok = false
		}
		return ok
	})
	return ok && hasType
}
