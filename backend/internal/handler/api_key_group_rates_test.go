package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetUserGroupRatesDoesNotExposeMultipliers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &APIKeyHandler{}
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
		c.Next()
	})
	router.GET("/groups/rates", h.GetUserGroupRates)

	req := httptest.NewRequest(http.MethodGet, "/groups/rates", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got struct {
		Code int                `json:"code"`
		Data map[string]float64 `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Empty(t, got.Data)
}
