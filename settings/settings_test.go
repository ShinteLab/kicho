package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// Load/Save は os.UserConfigDir() を使うため、テスト中は差し替える。
func useTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Windows は APPDATA、それ以外は XDG_CONFIG_HOME を見る。
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestLoadReturnsDefaultWhenMissing(t *testing.T) {
	useTempConfigDir(t)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != Default() {
		t.Errorf("Load() = %+v, want %+v", got, Default())
	}
	if got.Host != "127.0.0.1" || got.Port != 3000 {
		t.Errorf("default should be localhost:3000, got %+v", got)
	}
}

func TestSaveThenLoad(t *testing.T) {
	useTempConfigDir(t)

	want := Settings{Host: "0.0.0.0", Port: 8080, AutoStart: false}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

// Windows のメモ帳等は UTF-8 BOM を付けて保存する。手編集されても読めること。
func TestLoadAcceptsUTF8BOM(t *testing.T) {
	useTempConfigDir(t)

	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	body := append([]byte{0xEF, 0xBB, 0xBF},
		[]byte(`{"host":"0.0.0.0","port":3999,"autoStart":false}`)...)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load with BOM: %v", err)
	}
	want := Settings{Host: "0.0.0.0", Port: 3999, AutoStart: false}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadRejectsBrokenJSON(t *testing.T) {
	useTempConfigDir(t)

	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil {
		t.Error("Load() should fail on broken JSON rather than silently resetting")
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		in   Settings
		want Settings
	}{
		{Settings{Host: "", Port: 8080}, Settings{Host: "127.0.0.1", Port: 8080}},
		{Settings{Host: "0.0.0.0", Port: 0}, Settings{Host: "0.0.0.0", Port: 3000}},
		{Settings{Host: "0.0.0.0", Port: -1}, Settings{Host: "0.0.0.0", Port: 3000}},
		{Settings{Host: "0.0.0.0", Port: 70000}, Settings{Host: "0.0.0.0", Port: 3000}},
	}
	for _, c := range cases {
		if got := c.in.normalized(); got != c.want {
			t.Errorf("normalized(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestServerConfig(t *testing.T) {
	s := Settings{Host: "0.0.0.0", Port: 8080}
	c := s.ServerConfig()
	if c.Host != "0.0.0.0" || c.Port != 8080 {
		t.Errorf("ServerConfig() = %+v", c)
	}
	if !c.IsPublic() {
		t.Error("0.0.0.0 should be reported as public")
	}
}

func TestPaths(t *testing.T) {
	dir := useTempConfigDir(t)

	got, err := DatabasePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, AppName, databaseFile)
	if got != want {
		t.Errorf("DatabasePath() = %q, want %q", got, want)
	}

	// Dir() はディレクトリを作る。
	if _, err := os.Stat(filepath.Join(dir, AppName)); err != nil {
		t.Errorf("config dir was not created: %v", err)
	}
}
