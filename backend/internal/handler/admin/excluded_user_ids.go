package admin

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/gin-gonic/gin"
)

func parseExcludedUserIDs(c *gin.Context) ([]int64, error) {
	raw := strings.TrimSpace(c.Query("exclude_user_ids"))
	if raw == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("Invalid exclude_user_ids: %q", part)
		}
		ids = append(ids, id)
	}

	ids = usagestats.NormalizeExcludedUserIDs(ids)
	if len(ids) > usagestats.MaxExcludedUserIDs {
		return nil, fmt.Errorf("Invalid exclude_user_ids: maximum is %d", usagestats.MaxExcludedUserIDs)
	}
	return ids, nil
}
