package domain

import (
	"strconv"
	"strings"
)

// ExtractStackNameFromContainerName derives a stack name from a container name
// by splitting on "-" and taking the first segment. Trailing numeric instance
// suffixes (e.g. "-1") are stripped so "stack-service-1" returns "stack".
func ExtractStackNameFromContainerName(containerName string) string {
	if containerName == "" {
		return containerName
	}

	parts := strings.Split(containerName, "-")

	if len(parts) < 2 {
		return containerName
	}

	// Strip trailing instance number (e.g. myapp-web-1 -> myapp)
	if _, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
		if len(parts) >= 3 {
			return strings.Join(parts[:len(parts)-2], "-")
		}
		return parts[0]
	}

	return parts[0]
}
