package kubevirt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// fakeAPIServer serves the two plain-JSON discovery endpoints the
// client falls back to (/api core versions, /apis group list).
func fakeAPIServer(t *testing.T, groups ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			_ = json.NewEncoder(w).Encode(metav1.APIVersions{Versions: []string{"v1"}})
		case "/apis":
			list := metav1.APIGroupList{}
			for _, g := range groups {
				list.Groups = append(list.Groups, metav1.APIGroup{Name: g})
			}
			_ = json.NewEncoder(w).Encode(list)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDetectFindsTheKubeVirtGroup(t *testing.T) {
	srv := fakeAPIServer(t, "apps", Group)
	found, err := Detect(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !found {
		t.Fatal("kubevirt.io served but not detected")
	}
}

func TestDetectReportsAbsence(t *testing.T) {
	srv := fakeAPIServer(t, "apps")
	found, err := Detect(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if found {
		t.Fatal("kubevirt.io detected on a cluster not serving it")
	}
}

func TestDetectSurfacesDiscoveryErrors(t *testing.T) {
	srv := fakeAPIServer(t)
	srv.Close()
	if _, err := Detect(&rest.Config{Host: srv.URL}); err == nil {
		t.Fatal("unreachable API server must error, not report false")
	}
}
