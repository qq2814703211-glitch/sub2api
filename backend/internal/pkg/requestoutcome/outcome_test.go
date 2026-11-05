package requestoutcome

import "testing"

func TestOutcomeDistinguishesHTTP200ProtocolFailure(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"completed", "data: {\"type\":\"response.completed\"}\n\n", "completed"},
		{"failed", "data: {\"type\":\"error\"}\n\n", "failed"},
		{"incomplete", "data: {\"type\":\"response.created\"}\n\n", "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := NewOutcomeObserver("text/event-stream")
			for _, b := range []byte(tc.body) { o.Write([]byte{b}) }
			if got := o.Finish(); got != tc.want { t.Fatalf("got %q want %q", got, tc.want) }
		})
	}
}
