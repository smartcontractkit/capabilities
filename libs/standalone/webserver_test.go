package standalone

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthHandler(t *testing.T) {
	t.Parallel()

	healthy := func() (bool, map[string]error) {
		return true, map[string]error{"a": nil, "b": nil}
	}
	unhealthy := func() (bool, map[string]error) {
		return false, map[string]error{"a": nil, "b": errors.New("boom")}
	}

	for _, tt := range []struct {
		name     string
		check    func() (bool, map[string]error)
		target   string
		wantCode int
		wantBody string
	}{
		{
			name:     "healthy",
			check:    healthy,
			target:   "/livez",
			wantCode: http.StatusOK,
			wantBody: "ok\n",
		},
		{
			name:     "healthy verbose",
			check:    healthy,
			target:   "/livez?verbose",
			wantCode: http.StatusOK,
			wantBody: "[+]a ok\n[+]b ok\nlivez check passed\n",
		},
		{
			name:     "unhealthy",
			check:    unhealthy,
			target:   "/healthz",
			wantCode: http.StatusServiceUnavailable,
			wantBody: "b: boom\n",
		},
		{
			name:     "unhealthy verbose",
			check:    unhealthy,
			target:   "/healthz?verbose=true",
			wantCode: http.StatusServiceUnavailable,
			wantBody: "[+]a ok\n[-]b failed: boom\nhealthz check failed\n",
		},
		{
			name:     "verbose explicitly off",
			check:    healthy,
			target:   "/readyz?verbose=false",
			wantCode: http.StatusOK,
			wantBody: "ok\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			healthHandler(tt.check)(rec, httptest.NewRequest(http.MethodGet, tt.target, nil))

			require.Equal(t, tt.wantCode, rec.Code)
			assert.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}
