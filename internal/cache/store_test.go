package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreLoadHonorsTTL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := Store{Dir: dir, TTL: time.Minute}
	want := []string{"i-1", "i-2"}
	if err := store.Save("instances-us-east-1", want); err != nil {
		t.Fatal(err)
	}

	var got []string
	hit, err := store.Load("instances-us-east-1", &got)
	if err != nil || !hit || len(got) != len(want) {
		t.Fatalf("fresh Load() = %#v, %v, %v", got, hit, err)
	}

	path := filepath.Join(dir, "instances-us-east-1.json")
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	hit, err = store.Load("instances-us-east-1", &got)
	if err != nil || hit {
		t.Fatalf("expired Load() hit = %v, err = %v", hit, err)
	}
}
