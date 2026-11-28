package httpapi

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"testing"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
)

func TestIssueImagesReadAndCopy(t *testing.T) {
	t.Setenv("AGENTFLOW_AUTH_REQUIRED", "true")
	f := cvBNewHTTPFixture(t)
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	description := "# Checkout\n\n- Keep **existing** orders"
	saved := domain.Issue{ID: "iss-image", ProjectID: "platform", Title: "Checkout", Description: description}
	image := repo.IssueImage{ID: "img-checkout", Name: "checkout.png", MediaType: "image/png", Data: pngData.Bytes()}
	if err := f.s.CreateIssueWithImages(saved, []repo.IssueImage{image}); err != nil {
		t.Fatal(err)
	}
	path := "/api/issues/" + saved.ID
	rec := cvBRequest(t, f, http.MethodGet, path, nil, true)
	var detail struct {
		Images []repo.IssueImage `json:"images"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &detail) != nil || len(detail.Images) != 1 || detail.Images[0].Name != "checkout.png" {
		t.Fatalf("issue detail: %d %s", rec.Code, rec.Body.String())
	}
	imageID := detail.Images[0].ID
	imagePath := path + "/images/" + imageID
	if rec := cvBRequest(t, f, http.MethodGet, imagePath, nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated image: %d", rec.Code)
	}
	rec = cvBRequest(t, f, http.MethodGet, imagePath, nil, true)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rec.Body.Bytes(), pngData.Bytes()) {
		t.Fatalf("image response: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	trackerIssue := saved
	trackerIssue.ID = "tapd-test-image"
	if err := f.s.PutIssueWithCopiedImages(trackerIssue, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeleteIssue(saved.ID); err != nil {
		t.Fatal(err)
	}
	if images, err := f.s.IssueImages(saved.ID); err != nil || len(images) != 0 {
		t.Fatalf("deleted issue left images: %v %v", images, err)
	}
	trackerImagePath := "/api/issues/" + trackerIssue.ID + "/images/" + imageID
	if rec := cvBRequest(t, f, http.MethodGet, trackerImagePath, nil, true); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), pngData.Bytes()) {
		t.Fatalf("copied image: %d", rec.Code)
	}
}
