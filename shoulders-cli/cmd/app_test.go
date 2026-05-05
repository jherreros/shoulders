package cmd

import "testing"

func TestParseImageTag(t *testing.T) {
	image, tag := parseImageTag("nginx:1.26", "")
	if image != "nginx" || tag != "1.26" {
		t.Fatalf("expected nginx:1.26, got %s:%s", image, tag)
	}

	_, tag = parseImageTag("nginx", "")
	if tag != "latest" {
		t.Fatalf("expected latest, got %s", tag)
	}

	_, tag = parseImageTag("nginx", "custom")
	if tag != "custom" {
		t.Fatalf("expected custom, got %s", tag)
	}

	image, tag = parseImageTag("localhost:5000/team/api:dev", "")
	if image != "localhost:5000/team/api" || tag != "dev" {
		t.Fatalf("expected localhost registry image to parse, got %s:%s", image, tag)
	}
}

func TestParseEnvVars(t *testing.T) {
	env, err := parseEnvVars([]string{"LOG_LEVEL=debug", "EMPTY="})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(env) != 2 || env[0]["name"] != "LOG_LEVEL" || env[0]["value"] != "debug" {
		t.Fatalf("unexpected env parse result: %#v", env)
	}
}

func TestBuildVolumesAndMounts(t *testing.T) {
	volumes, mounts, err := buildVolumesAndMounts([]string{"jwt-secret:/keys:jwt"}, []string{"tmp:/tmp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(volumes) != 2 || len(mounts) != 2 {
		t.Fatalf("expected two volumes and mounts, got %#v %#v", volumes, mounts)
	}
}
