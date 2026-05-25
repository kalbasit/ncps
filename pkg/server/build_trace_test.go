package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testhelper"
)

const (
	btDrvName    = "qwwz2sxy84n4slkyff4jirbihqk3qvhf-skopeo-1.21.0.drv"
	btOutputName = "out"
	btOutPath    = "/nix/store/xyz000000000000000000000000000000-skopeo-1.21.0"
)

// btValidBody returns a JSON-encoded build trace entry for use in tests.
func btValidBody() string {
	entry := map[string]any{
		"key": map[string]any{
			"drvPath":    "/nix/store/" + btDrvName,
			"outputName": btOutputName,
		},
		"value": map[string]any{
			"outPath": btOutPath,
		},
	}

	b, err := json.Marshal(entry)
	if err != nil {
		panic(err)
	}

	return string(b)
}

// setupBuildTraceTest returns a running httptest.Server with a fresh local
// cache and optionally with PUT permitted.
func setupBuildTraceTest(t *testing.T, putPermitted bool) (*http.Client, string) {
	t.Helper()

	dir, err := os.MkdirTemp("", "cache-build-trace-")
	require.NoError(t, err)

	t.Cleanup(func() { os.RemoveAll(dir) })

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	dbClient, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), dbClient, localStore, localStore, localStore)
	require.NoError(t, err)

	s := server.New(c)
	s.SetPutPermitted(putPermitted)

	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	return ts.Client(), ts.URL
}

// ── GET /build-trace-v2/{drvName}/{outputName} ──────────────────────────────

func TestGetBuildTrace_NotFound(t *testing.T) {
	t.Parallel()

	client, baseURL := setupBuildTraceTest(t, false)

	path := "/build-trace-v2/" + btDrvName + "/" + btOutputName
	req, err := http.NewRequestWithContext(newContext(), http.MethodGet, baseURL+path, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestGetBuildTrace_Found(t *testing.T) {
	t.Parallel()

	client, baseURL := setupBuildTraceTest(t, true)

	// PUT first
	uploadPath := "/upload/build-trace-v2/" + btDrvName + "/" + btOutputName
	putReq, err := http.NewRequestWithContext(newContext(), http.MethodPut,
		baseURL+uploadPath, strings.NewReader(btValidBody()))
	require.NoError(t, err)

	putResp, err := client.Do(putReq)
	require.NoError(t, err)
	putResp.Body.Close()
	require.Equal(t, http.StatusNoContent, putResp.StatusCode)

	// GET
	getPath := "/build-trace-v2/" + btDrvName + "/" + btOutputName
	getReq, err := http.NewRequestWithContext(newContext(), http.MethodGet, baseURL+getPath, nil)
	require.NoError(t, err)

	getResp, err := client.Do(getReq)
	require.NoError(t, err)

	defer getResp.Body.Close()

	assert.Equal(t, http.StatusOK, getResp.StatusCode)
	assert.Equal(t, "application/json", getResp.Header.Get("Content-Type"))

	var result struct {
		Value struct {
			OutPath string `json:"outPath"`
		} `json:"value"`
	}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&result))
	assert.Equal(t, btOutPath, result.Value.OutPath)
}

// ── HEAD /build-trace-v2/{drvName}/{outputName} ─────────────────────────────

func TestHeadBuildTrace_NotFound(t *testing.T) {
	t.Parallel()

	client, baseURL := setupBuildTraceTest(t, false)

	path := "/build-trace-v2/" + btDrvName + "/" + btOutputName
	req, err := http.NewRequestWithContext(newContext(), http.MethodHead, baseURL+path, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHeadBuildTrace_Found(t *testing.T) {
	t.Parallel()

	client, baseURL := setupBuildTraceTest(t, true)

	// PUT first
	uploadPath := "/upload/build-trace-v2/" + btDrvName + "/" + btOutputName
	putReq, err := http.NewRequestWithContext(newContext(), http.MethodPut,
		baseURL+uploadPath, strings.NewReader(btValidBody()))
	require.NoError(t, err)

	putResp, err := client.Do(putReq)
	require.NoError(t, err)
	putResp.Body.Close()
	require.Equal(t, http.StatusNoContent, putResp.StatusCode)

	// HEAD
	headPath := "/build-trace-v2/" + btDrvName + "/" + btOutputName
	headReq, err := http.NewRequestWithContext(newContext(), http.MethodHead, baseURL+headPath, nil)
	require.NoError(t, err)

	headResp, err := client.Do(headReq)
	require.NoError(t, err)

	defer headResp.Body.Close()

	assert.Equal(t, http.StatusOK, headResp.StatusCode)
}

// ── PUT /upload/build-trace-v2/{drvName}/{outputName} ───────────────────────

func TestPutBuildTrace_Success(t *testing.T) {
	t.Parallel()

	client, baseURL := setupBuildTraceTest(t, true)

	path := "/upload/build-trace-v2/" + btDrvName + "/" + btOutputName
	req, err := http.NewRequestWithContext(newContext(), http.MethodPut,
		baseURL+path, strings.NewReader(btValidBody()))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestPutBuildTrace_PutNotPermitted(t *testing.T) {
	t.Parallel()

	client, baseURL := setupBuildTraceTest(t, false)

	path := "/upload/build-trace-v2/" + btDrvName + "/" + btOutputName
	req, err := http.NewRequestWithContext(newContext(), http.MethodPut,
		baseURL+path, strings.NewReader(btValidBody()))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestPutBuildTrace_BadBody(t *testing.T) {
	t.Parallel()

	client, baseURL := setupBuildTraceTest(t, true)

	path := "/upload/build-trace-v2/" + btDrvName + "/" + btOutputName
	req, err := http.NewRequestWithContext(newContext(), http.MethodPut,
		baseURL+path, strings.NewReader("not json"))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPutBuildTrace_URLBodyMismatch(t *testing.T) {
	t.Parallel()

	client, baseURL := setupBuildTraceTest(t, true)

	// URL says "wrong-name.drv" but body has btDrvName.
	path := "/upload/build-trace-v2/wrong-name.drv/" + btOutputName
	req, err := http.NewRequestWithContext(newContext(), http.MethodPut,
		baseURL+path, strings.NewReader(btValidBody()))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestGetBuildTrace_DotDoiSuffix verifies that outputName with a .doi suffix is
// transparently stripped before the lookup.
func TestGetBuildTrace_DotDoiSuffix(t *testing.T) {
	t.Parallel()

	client, baseURL := setupBuildTraceTest(t, true)

	// PUT without .doi
	uploadPath := "/upload/build-trace-v2/" + btDrvName + "/" + btOutputName
	putReq, err := http.NewRequestWithContext(newContext(), http.MethodPut,
		baseURL+uploadPath, strings.NewReader(btValidBody()))
	require.NoError(t, err)

	putResp, err := client.Do(putReq)
	require.NoError(t, err)
	putResp.Body.Close()
	require.Equal(t, http.StatusNoContent, putResp.StatusCode)

	// GET with .doi suffix — should still resolve.
	getPath := "/build-trace-v2/" + btDrvName + "/" + btOutputName + ".doi"
	getReq, err := http.NewRequestWithContext(newContext(), http.MethodGet, baseURL+getPath, nil)
	require.NoError(t, err)

	getResp, err := client.Do(getReq)
	require.NoError(t, err)

	defer getResp.Body.Close()

	assert.Equal(t, http.StatusOK, getResp.StatusCode)
}
