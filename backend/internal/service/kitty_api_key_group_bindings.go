package service

func (k *APIKey) ConfiguredGroupIDs() []int64 {
	if k == nil {
		return nil
	}
	return normalizeAPIKeyGroupIDs(k.GroupID, k.GroupIDs)
}

func normalizeAPIKeyGroupIDs(primary *int64, extras []int64) []int64 {
	seen := make(map[int64]struct{}, len(extras)+1)
	out := make([]int64, 0, len(extras)+1)
	if primary != nil && *primary > 0 {
		seen[*primary] = struct{}{}
		out = append(out, *primary)
	}
	for _, id := range extras {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
