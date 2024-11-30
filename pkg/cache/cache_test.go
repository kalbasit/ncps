package cache

import "testing"

func TestPublicKey(t *testing.T) {
	c, err := New("cache.example.com", "/tmp", "cache.example.com:SXVsbjV+SY2hcmnAlYU3y/UGk75zYFLfrr2+TZZrt8btk3i7/bqd44Cj9smdI8PfIZd4tuJwCtSzvVk2N1nkjw==")
	if err != nil {
		t.Fatalf("error not expected, got an error: %s", err)
	}

	if want, got := "cache.example.com:7ZN4u/26neOAo/bJnSPD3yGXeLbicArUs71ZNjdZ5I8=", c.PublicKey(); want != got {
		t.Errorf("want %q, got %q", want, got)
	}
}
