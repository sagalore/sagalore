package main

import "testing"

func TestRejectDevSecrets(t *testing.T) {
	t.Setenv("SAGALORE_ALLOW_DEV_SECRETS", "")
	t.Setenv("ADMIN_TOKEN", "dev-admin-token")
	t.Setenv("IMAGE_URL_SECRET", "real-secret")
	if err := rejectDevSecrets(); err == nil {
		t.Fatal("expected rejection of docker-compose ADMIN_TOKEN")
	}

	t.Setenv("ADMIN_TOKEN", "real-admin")
	t.Setenv("IMAGE_URL_SECRET", "dev-image-url-secret")
	if err := rejectDevSecrets(); err == nil {
		t.Fatal("expected rejection of docker-compose IMAGE_URL_SECRET")
	}

	t.Setenv("ADMIN_TOKEN", "real-admin")
	t.Setenv("IMAGE_URL_SECRET", "real-secret")
	if err := rejectDevSecrets(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SAGALORE_ALLOW_DEV_SECRETS", "1")
	t.Setenv("ADMIN_TOKEN", "dev-admin-token")
	t.Setenv("IMAGE_URL_SECRET", "dev-image-url-secret")
	if err := rejectDevSecrets(); err != nil {
		t.Fatal(err)
	}
}
