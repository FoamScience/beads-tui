package bd

import (
	"regexp"
	"strings"
	"time"
)

// A live ledger is a pinned bead labelled "live" whose description holds a summary, a
// "= Current state =" table and a dated "= Log =" (see the beads skill, "Live ledgers").

func (i *Issue) IsLedger() bool { return i.Status == "pinned" && i.HasLabel("live") }

type LedgerRow struct{ Key, Value string }

type LedgerEntry struct {
	Date time.Time
	Text string
}

type Ledger struct {
	Summary  string
	State    []LedgerRow
	Log      []LedgerEntry // in file order, oldest first
	Problems []string      // ways the description departs from the ledger format
}

var (
	ledgerHeading = regexp.MustCompile(`^=\s*(.+?)\s*=$`)
	ledgerEntry   = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}):\s*(.*)$`)
)

func ParseLedger(desc string) Ledger {
	var l Ledger
	var summary []string
	section := ""
	seen := map[string]bool{}
	tables := 0
	inTable := false
	for _, raw := range strings.Split(desc, "\n") {
		line := strings.TrimSpace(raw)
		if m := ledgerHeading.FindStringSubmatch(line); m != nil {
			section = strings.ToLower(m[1])
			seen[section] = true
			inTable = false
			continue
		}
		switch section {
		case "":
			if line != "" {
				summary = append(summary, line)
			}
		case "current state":
			if !strings.HasPrefix(line, "|") {
				inTable = false
				if line != "" {
					l.Problems = append(l.Problems, "text outside the table in Current state")
				}
				continue
			}
			if !inTable {
				inTable = true
				tables++
				continue // header row
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			if len(cells) < 2 || strings.Trim(cells[0], " -:") == "" {
				continue // separator row
			}
			l.State = append(l.State, LedgerRow{strings.TrimSpace(cells[0]), strings.TrimSpace(strings.Join(cells[1:], "|"))})
		case "log":
			if line == "" {
				continue
			}
			if m := ledgerEntry.FindStringSubmatch(line); m != nil {
				d, _ := time.Parse("2006-01-02", m[1])
				l.Log = append(l.Log, LedgerEntry{d, m[2]})
			} else if n := len(l.Log); n > 0 {
				l.Log[n-1].Text += " " + line
			} else {
				l.Problems = append(l.Problems, "log line without a YYYY-MM-DD date")
			}
		default:
			if line != "" {
				l.Problems = append(l.Problems, "unexpected section "+section)
				section = "?"
			}
		}
	}
	l.Summary = strings.Join(summary, " ")
	if !seen["current state"] {
		l.Problems = append(l.Problems, "no = Current state = section")
	}
	if !seen["log"] {
		l.Problems = append(l.Problems, "no = Log = section")
	}
	if tables > 1 {
		l.Problems = append(l.Problems, "more than one table in Current state")
	}
	return l
}

// Last is the date of the newest log entry, zero when there is none.
func (l Ledger) Last() time.Time {
	var t time.Time
	for _, e := range l.Log {
		if e.Date.After(t) {
			t = e.Date
		}
	}
	return t
}

// AppendLog returns the description with one more dated log line, keeping the rest verbatim.
func AppendLog(desc string, day time.Time, text string) string {
	entry := day.Format("2006-01-02") + ": " + text
	lines := strings.Split(strings.TrimRight(desc, "\n"), "\n")
	inLog, last := false, -1
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if m := ledgerHeading.FindStringSubmatch(line); m != nil {
			inLog = strings.EqualFold(m[1], "log")
			if inLog {
				last = i
			}
			continue
		}
		if inLog && line != "" {
			last = i
		}
	}
	if last < 0 {
		return strings.TrimRight(desc, "\n") + "\n\n= Log =\n\n" + entry + "\n"
	}
	out := append(append(append([]string{}, lines[:last+1]...), entry), lines[last+1:]...)
	return strings.Join(out, "\n") + "\n"
}
