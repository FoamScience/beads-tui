package bd

import (
	"strings"
	"testing"
	"time"
)

const sampleLedger = `Tracks how many impellers parametrize.

= Current state =

| Item | Value |
|---|---|
| Impellers parametrized | 24 / 38 |
| Shrouded | H +0.5 % [-11, +22] |

= Log =

2026-10-08: campaign built; 13/38 parametrized.
2026-10-09: junction fixes: 24/38 parametrized,
  224 points.
`

func TestParseLedger(t *testing.T) {
	l := ParseLedger(sampleLedger)
	if l.Summary != "Tracks how many impellers parametrize." {
		t.Errorf("summary %q", l.Summary)
	}
	if len(l.State) != 2 || l.State[0] != (LedgerRow{"Impellers parametrized", "24 / 38"}) {
		t.Errorf("state %+v", l.State)
	}
	if len(l.Log) != 2 || !strings.HasSuffix(l.Log[1].Text, "224 points.") {
		t.Errorf("log %+v", l.Log)
	}
	if got := l.Last().Format("2006-01-02"); got != "2026-10-09" {
		t.Errorf("last %s", got)
	}
	if len(l.Problems) != 0 {
		t.Errorf("valid ledger reported problems: %v", l.Problems)
	}
	bad := ParseLedger("summary\n\n= Current state =\n\nloose text\n\n= Notes =\nx\n")
	if len(bad.Problems) < 3 {
		t.Errorf("broken ledger should report problems, got %v", bad.Problems)
	}
}

func TestAppendLogGoesAfterTheLastEntry(t *testing.T) {
	out := AppendLog(sampleLedger, time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), "fluid names resolved")
	l := ParseLedger(out)
	if len(l.Log) != 3 || l.Log[2].Text != "fluid names resolved" {
		t.Fatalf("appended log: %+v\n%s", l.Log, out)
	}
	if !strings.HasPrefix(out, "Tracks how many impellers parametrize.\n\n= Current state =") {
		t.Errorf("rest of the description changed:\n%s", out)
	}
}
