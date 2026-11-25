package lifecyclemodule

import (
	"fmt"
	"strconv"
	"strings"
)

type cleanupPosition struct {
	spec      int
	timestamp int64
	id        any
}

func (p cleanupPosition) checkpoint() string {
	if id, ok := p.id.(int64); ok {
		return fmt.Sprintf("%d:%d:i:%d", p.spec, p.timestamp, id)
	}
	return fmt.Sprintf("%d:%d:s:%s", p.spec, p.timestamp, p.id)
}

func parseCleanupCheckpoint(value string, specs int) (cleanupPosition, error) {
	if value == "" {
		return cleanupPosition{}, nil
	}
	parts := strings.SplitN(value, ":", 4)
	if len(parts) != 4 || parts[3] == "" {
		return cleanupPosition{}, fmt.Errorf("invalid lifecycle cleanup checkpoint")
	}
	spec, specErr := strconv.Atoi(parts[0])
	timestamp, timeErr := strconv.ParseInt(parts[1], 10, 64)
	if specErr != nil || spec < 0 || spec >= specs || timeErr != nil {
		return cleanupPosition{}, fmt.Errorf("invalid lifecycle cleanup checkpoint position")
	}
	position := cleanupPosition{spec: spec, timestamp: timestamp, id: parts[3]}
	switch parts[2] {
	case "s":
	case "i":
		id, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			return cleanupPosition{}, fmt.Errorf("invalid lifecycle cleanup checkpoint numeric ID")
		}
		position.id = id
	default:
		return cleanupPosition{}, fmt.Errorf("invalid lifecycle cleanup checkpoint ID type")
	}
	return position, nil
}
