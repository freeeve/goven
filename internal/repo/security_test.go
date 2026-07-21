package repo

import (
	"os"
	"path/filepath"
	"testing"
)

// Vectors generated with Apache Maven 3.9.11:
//
//	mvn --encrypt-master-password testmaster123
//	mvn --encrypt-password s3cretPW   (against that master)
const (
	vectorMasterBlob  = "{k4PWf+257SsHsCsetHVFLGFlv87nfj536j6aaPiZoBw=}"
	vectorMasterPlain = "testmaster123"
	vectorServerBlob  = "{3v/qcbEnsM0HXz4bFZn8yBHVPwiI+cxzHm9TlQU4s3o=}"
	vectorServerPlain = "s3cretPW"
)

func TestDecryptPasswordMavenVectors(t *testing.T) {
	master, err := DecryptPassword(vectorMasterBlob, masterPasswordKey)
	if err != nil {
		t.Fatalf("decrypt master: %v", err)
	}
	if master != vectorMasterPlain {
		t.Fatalf("master = %q, want %q", master, vectorMasterPlain)
	}
	pw, err := DecryptPassword(vectorServerBlob, master)
	if err != nil {
		t.Fatalf("decrypt server password: %v", err)
	}
	if pw != vectorServerPlain {
		t.Fatalf("password = %q, want %q", pw, vectorServerPlain)
	}
}

func TestDecryptPasswordWrongKey(t *testing.T) {
	if _, err := DecryptPassword(vectorServerBlob, "not-the-master"); err == nil {
		t.Error("expected error with wrong passphrase")
	}
}

func TestDecryptPasswordGarbage(t *testing.T) {
	for _, v := range []string{"{not base64!}", "{QUJD}", ""} {
		if _, err := DecryptPassword(v, "x"); err == nil {
			t.Errorf("DecryptPassword(%q) should fail", v)
		}
	}
}

func TestLoadMasterPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings-security.xml")
	if err := os.WriteFile(path,
		[]byte("<settingsSecurity><master>"+vectorMasterBlob+"</master></settingsSecurity>"), 0o600); err != nil {
		t.Fatal(err)
	}
	master, err := LoadMasterPassword(path)
	if err != nil {
		t.Fatal(err)
	}
	if master != vectorMasterPlain {
		t.Errorf("master = %q", master)
	}
	if m, err := LoadMasterPassword(filepath.Join(dir, "absent.xml")); err != nil || m != "" {
		t.Errorf("absent file: master=%q err=%v, want empty/nil", m, err)
	}
}

func TestExtractEncrypted(t *testing.T) {
	cases := []struct {
		in    string
		inner string
		ok    bool
	}{
		{"{abc=}", "abc=", true},
		{"prefix {abc=} suffix", "abc=", true},
		{"plaintext", "", false},
		{`\{literal}`, "", false},
		{"{}", "", true},
	}
	for _, tc := range cases {
		inner, ok := extractEncrypted(tc.in)
		if ok != tc.ok || inner != tc.inner {
			t.Errorf("extractEncrypted(%q) = %q,%v want %q,%v", tc.in, inner, ok, tc.inner, tc.ok)
		}
	}
}

func TestResolvePassword(t *testing.T) {
	if pw, err := ResolvePassword("plain", ""); err != nil || pw != "plain" {
		t.Errorf("plaintext passthrough: %q %v", pw, err)
	}
	if _, err := ResolvePassword(vectorServerBlob, ""); err == nil {
		t.Error("encrypted without master must error")
	}
	if pw, err := ResolvePassword(vectorServerBlob, vectorMasterPlain); err != nil || pw != vectorServerPlain {
		t.Errorf("decrypt: %q %v", pw, err)
	}
}

func TestEffectiveReposDecryptsCredentials(t *testing.T) {
	s := &Settings{
		Master:  vectorMasterPlain,
		Servers: []Server{{ID: "nexus", Username: "deployer", Password: vectorServerBlob}},
		Profiles: []Profile{{ID: "p", ActiveByDefault: true,
			Repositories: []Repository{{ID: "nexus", URL: "https://n/repo", Releases: true}}}},
	}
	repos, err := EffectiveRepos(s, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if repos[0].Password != vectorServerPlain {
		t.Errorf("password = %q, want decrypted %q", repos[0].Password, vectorServerPlain)
	}

	s.Master = "wrong-master"
	if _, err := EffectiveRepos(s, nil, nil); err == nil {
		t.Error("wrong master must surface an error, not silent empty credentials")
	}
}

func FuzzDecryptPassword(f *testing.F) {
	f.Add(vectorMasterBlob, masterPasswordKey)
	f.Add(vectorServerBlob, vectorMasterPlain)
	f.Add("{AAAA}", "k")
	f.Fuzz(func(t *testing.T, blob, key string) {
		DecryptPassword(blob, key)
	})
}
