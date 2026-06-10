package hosts

import "testing"

func TestMergeSource(t *testing.T) {
	cases := []struct{ existing, incoming, want string }{
		{"", SourceAgent, SourceAgent},
		{"", SourceConnector, SourceAgentConnector}, // legacy "" means agent
		{SourceAgent, SourceConnector, SourceAgentConnector},
		{SourceConnector, SourceAgent, SourceAgentConnector},
		{SourceConnector, SourceConnector, SourceConnector},
		{SourceAgentConnector, SourceAgent, SourceAgentConnector},
		{SourceAgentConnector, SourceConnector, SourceAgentConnector},
		{SourceAgent, "", SourceAgent},
	}
	for _, c := range cases {
		if got := MergeSource(c.existing, c.incoming); got != c.want {
			t.Errorf("MergeSource(%q,%q) = %q, want %q", c.existing, c.incoming, got, c.want)
		}
	}
}
