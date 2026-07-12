package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNormalizeExcludedUserIDs(t *testing.T) {
	got := usagestats.NormalizeExcludedUserIDs([]int64{3, 0, 1, -2, 3, 2})

	require.Equal(t, []int64{1, 2, 3}, got)
}

func TestParseExcludedUserIDs_Normalizes(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/?exclude_user_ids=3%2C1%2C3%2C2", nil)

	got, err := parseExcludedUserIDs(c)

	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, got)
}

func TestParseExcludedUserIDs_EmptyInput(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "absent", url: "/"},
		{name: "blank", url: "/?exclude_user_ids="},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, tt.url, nil)

			got, err := parseExcludedUserIDs(c)

			require.NoError(t, err)
			require.Nil(t, got)
		})
	}
}

func TestParseExcludedUserIDs_RejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "text", raw: "one"},
		{name: "zero", raw: "0"},
		{name: "negative", raw: "-1"},
		{name: "empty segment", raw: "1,,2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/?exclude_user_ids="+tt.raw, nil)

			got, err := parseExcludedUserIDs(c)

			require.ErrorContains(t, err, "Invalid exclude_user_ids")
			require.Nil(t, got)
		})
	}
}

func TestParseExcludedUserIDs_RejectsMoreThan100UniqueIDs(t *testing.T) {
	parts := make([]string, 101)
	for i := range parts {
		parts[i] = fmt.Sprintf("%d", i+1)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/?exclude_user_ids="+strings.Join(parts, ","),
		nil,
	)

	got, err := parseExcludedUserIDs(c)

	require.ErrorContains(t, err, "Invalid exclude_user_ids")
	require.Nil(t, got)
}

func TestParseExcludedUserIDs_Accepts100UniqueIDs(t *testing.T) {
	parts := make([]string, usagestats.MaxExcludedUserIDs)
	for i := range parts {
		parts[i] = fmt.Sprintf("%d", i+1)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/?exclude_user_ids="+strings.Join(parts, ","),
		nil,
	)

	got, err := parseExcludedUserIDs(c)

	require.NoError(t, err)
	require.Len(t, got, usagestats.MaxExcludedUserIDs)
}
