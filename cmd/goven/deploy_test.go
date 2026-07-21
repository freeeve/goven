package main

import (
	"testing"
)

func TestAttachListSet(t *testing.T) {
	cases := []struct {
		in        string
		file, cls string
		typ       string
		err       bool
	}{
		{in: "lib-sources.jar:sources", file: "lib-sources.jar", cls: "sources", typ: "jar"},
		{in: "dist/lib.tar.gz:dist:tar.gz", file: "dist/lib.tar.gz", cls: "dist", typ: "tar.gz"},
		{in: "a/b/c.jar:javadoc", file: "a/b/c.jar", cls: "javadoc", typ: "jar"},
		{in: "nocls", err: true},
		{in: ":sources", err: true},
	}
	for _, tc := range cases {
		var a attachList
		err := a.Set(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("Set(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("Set(%q): %v", tc.in, err)
			continue
		}
		att := a[0]
		if att.File != tc.file || att.Classifier != tc.cls || att.Type != tc.typ {
			t.Errorf("Set(%q) = %+v, want %s/%s/%s", tc.in, att, tc.file, tc.cls, tc.typ)
		}
	}
}

func TestAttachListSideFiles(t *testing.T) {
	var a attachList
	props := map[string]string{
		"files":       "s.jar,d.tar",
		"classifiers": "sources,dist",
		"types":       "jar,tar",
	}
	if err := a.addSideFiles(props); err != nil {
		t.Fatal(err)
	}
	if len(a) != 2 || a[1].Classifier != "dist" || a[1].Type != "tar" {
		t.Errorf("attachments = %+v", a)
	}

	var b attachList
	if err := b.addSideFiles(map[string]string{"files": "a,b", "classifiers": "one", "types": "jar,jar"}); err == nil {
		t.Error("mismatched list lengths must error")
	}
}
