package projects

func firstStorageID(primary, fallback *int) *int {
	if primary != nil {
		return primary
	}
	return fallback
}
