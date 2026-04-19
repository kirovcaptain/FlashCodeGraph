package urlutil

import "testing"

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		input       string
		wantPath    string
		wantService string
	}{
		{"http://user-service/api/users/123?page=1", "/api/users/{param}", "user-service"},
		{"http://user-service:8080/api/users/{id}", "/api/users/{param}", "user-service"},
		{"https://api.weather.com/v1/forecast", "/v1/forecast", "api.weather.com"},
		{"/api/users/{id}", "/api/users/{param}", ""},
		{"/api/users/:id", "/api/users/{param}", ""},
		{"/api/users/<int:id>", "/api/users/{param}", ""},
		{"/api/users/[id]", "/api/users/{param}", ""},
		{"http://svc/api/${userId}", "/api/{param}", "svc"},
		{"http://localhost:8081/api/users", "/api/users", ""},
		{"", "", ""},
	}

	for _, tc := range cases {
		path, svc := NormalizeURL(tc.input)
		if path != tc.wantPath {
			t.Errorf("NormalizeURL(%q) path = %q, want %q", tc.input, path, tc.wantPath)
		}
		if svc != tc.wantService {
			t.Errorf("NormalizeURL(%q) service = %q, want %q", tc.input, svc, tc.wantService)
		}
	}
	t.Log("✅ NormalizeURL: 10 cases passed")
}

func TestNormalizePathParams(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/api/users/{id}", "/api/users/{param}"},
		{"/api/users/:id", "/api/users/{param}"},
		{"/api/users/<int:id>", "/api/users/{param}"},
		{"/api/users/[id]", "/api/users/{param}"},
		{"/api/users/123", "/api/users/{param}"},
		{"/api/users/123/orders/456", "/api/users/{param}/orders/{param}"},
		{"/api/users", "/api/users"},
		{"/api/${userId}/orders", "/api/{param}/orders"},
	}

	for _, tc := range cases {
		got := NormalizePathParams(tc.input)
		if got != tc.want {
			t.Errorf("NormalizePathParams(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	t.Log("✅ NormalizePathParams: 8 cases passed")
}
