package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"/home/u/.claude":         "claude",
		"/home/u/.claude-melle":   "claude-melle",
		"/home/u/.claude-Work_2":  "claude-work-2",
		"/home/u/.claude--x--y":   "claude-x-y",
		"/home/u/.claude-멜레":      "claude", // 비ASCII만 남으면 fallback
		"/home/u/.CLAUDE-Foo.Bar": "claude-foo-bar",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q)=%q want %q", in, got, want)
		}
	}
}

func TestTokenKeyFor(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("USERPROFILE", "/home/u")
	if got := TokenKeyFor("/home/u/.claude"); got != "device-token" {
		t.Errorf("default key = %q want device-token", got)
	}
	if got := TokenKeyFor("/home/u/.claude-melle"); got != "device-token-claude-melle" {
		t.Errorf("melle key = %q", got)
	}
}

func TestListInjectsDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	r := New(filepath.Join(home, "registry.json"))

	// 파일 없음 → 기본 폴더 단일 항목(하위호환)
	es, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || !IsDefault(es[0].ConfigDir) {
		t.Fatalf("missing-file List = %+v, want single default", es)
	}

	// melle만 등록해도 List엔 default가 prepend돼야(기본 폴더 수집 계속)
	if _, err := r.Upsert(filepath.Join(home, ".claude-melle")); err != nil {
		t.Fatal(err)
	}
	es, _ = r.List()
	if len(es) != 2 || !IsDefault(es[0].ConfigDir) {
		t.Fatalf("List after upsert = %+v, want [default, melle]", es)
	}
}

func TestUpsertIdempotentAndCollision(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	r := New(filepath.Join(home, "registry.json"))

	e1, err := r.Upsert(filepath.Join(home, ".claude-melle"))
	if err != nil {
		t.Fatal(err)
	}
	// 같은 폴더 재등록 → 중복 항목 안 생김
	if _, err := r.Upsert(filepath.Join(home, ".claude-melle")); err != nil {
		t.Fatal(err)
	}
	raw, _ := r.loadRaw()
	if len(raw) != 1 || raw[0] != e1 {
		t.Fatalf("re-upsert produced %+v", raw)
	}

	// 다른 폴더가 같은 slug로 충돌 → 거부. registry.json을 직접 조작해 재현.
	bad := []Entry{{ConfigDir: "/other/place/.claude-melle", TokenKey: "device-token-claude-melle"}}
	if err := r.save(bad); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Upsert(filepath.Join(home, ".claude-melle")); err == nil {
		t.Fatal("expected slug collision error")
	}
}

func TestRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	r := New(filepath.Join(home, "registry.json"))

	melle := filepath.Join(home, ".claude-melle")
	if _, err := r.Upsert(melle); err != nil {
		t.Fatal(err)
	}
	removed, ok, err := r.Remove(melle)
	if err != nil || !ok || removed.TokenKey != "device-token-claude-melle" {
		t.Fatalf("Remove = %+v ok=%v err=%v", removed, ok, err)
	}
	if raw, _ := r.loadRaw(); len(raw) != 0 {
		t.Fatalf("registry not empty after remove: %+v", raw)
	}

	// 기본 폴더 forget 거부
	if _, _, err := r.Remove(DefaultConfigDir()); err == nil {
		t.Fatal("expected default-remove refusal")
	}

	// 없는 폴더 remove → found=false, 에러 없음
	if _, ok, err := r.Remove(filepath.Join(home, ".claude-nope")); ok || err != nil {
		t.Fatalf("remove missing: ok=%v err=%v", ok, err)
	}
}

// sanity: registry.json은 0600으로 저장
func TestSavePerms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	r := New(filepath.Join(home, "registry.json"))
	if _, err := r.Upsert(filepath.Join(home, ".claude-x")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(home, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v want 0600", fi.Mode().Perm())
	}
}
