package xmldecode

import "testing"

func TestRewriteEncodingDecl(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<?xml version="1.0" encoding="UTF-16"?><r/>`, `<?xml version="1.0" encoding="UTF-8"?><r/>`},
		{`<?xml version="1.0" encoding='UTF-16'?><r/>`, `<?xml version="1.0" encoding="UTF-8"?><r/>`},
		{`<?xml version="1.0"?><r/>`, `<?xml version="1.0"?><r/>`},
		{`<r/>`, `<r/>`},
	}
	for _, c := range cases {
		if got := rewriteEncodingDecl(c.in); got != c.want {
			t.Errorf("rewriteEncodingDecl(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
