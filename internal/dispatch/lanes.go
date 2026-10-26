// Package dispatch routes ready beads to lanes and works each in its own agent session, named after
// the bead, in a herdr pane.
package dispatch

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Lane is where beads are routed; it works them one at a time, each in a new session named after the bead.
type Lane struct {
	Name      string   `toml:"-"`
	Auto      bool     `toml:"-"`     // created by the decider, kept in lanes.auto.toml
	About     string   `toml:"about"` // what the lane works on, for the decider
	Cwd       string   `toml:"cwd"`
	Workspace string   `toml:"workspace"` // herdr workspace label to start the lane's bead sessions in
	Group     string   `toml:"group"`     // bt dispatch groups lanes by this, else by workspace
	Takes     Takes    `toml:"takes"`
	Owns      []string `toml:"owns"`
	Model     string   `toml:"model"`
}

// GroupName is the lane's group in bt dispatch: group, else workspace, else "other".
func (l *Lane) GroupName() string {
	switch {
	case l.Group != "":
		return l.Group
	case l.Workspace != "":
		return l.Workspace
	}
	return "other"
}

type Takes struct {
	Labels []string `toml:"labels"`
	Epics  []string `toml:"epics"`
}

// Decider is the model that routes the allowed beads the rules leave unrouted.
type Decider struct {
	Model string `toml:"model"`
}

// Auto lets the decider open a new lane when no configured lane fits, up to MaxLanes of them.
type Auto struct {
	MaxLanes  int    `toml:"max_lanes"`
	Cwd       string `toml:"cwd"`
	Workspace string `toml:"workspace"`
	Model     string `toml:"model"`
}

type Config struct {
	MaxActive int              `toml:"max_active"` // busy lanes at once; 0 means no cap
	Decider   *Decider         `toml:"decider"`
	Auto      *Auto            `toml:"auto"`
	Lanes     map[string]*Lane `toml:"lane"`
}

func ConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "bt", "lanes.toml")
}

// AutoPath is lanes.auto.toml next to lanes.toml at path.
func AutoPath(path string) string { return filepath.Join(filepath.Dir(path), "lanes.auto.toml") }

// LoadConfig reads lanes.toml plus the auto lanes bt dispatch keeps next to it in lanes.auto.toml.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := ParseConfig(string(b))
	if err != nil {
		return nil, err
	}
	b, err = os.ReadFile(AutoPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	a, err := ParseConfig(string(b))
	if err != nil {
		return nil, fmt.Errorf("lanes.auto.toml: %w", err)
	}
	for name, l := range a.Lanes {
		if _, dup := c.Lanes[name]; dup {
			return nil, fmt.Errorf("lane %s is in both lanes.toml and lanes.auto.toml", name)
		}
		l.Auto = true
		c.Lanes[name] = l
	}
	return c, nil
}

var (
	laneName    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	tableHeader = regexp.MustCompile(`^\[\s*([A-Za-z0-9_.-]+)\s*\]\s*(#.*)?$`)
)

// AutoLanes counts the lanes the decider created.
func (c *Config) AutoLanes() int {
	n := 0
	for _, l := range c.Lanes {
		if l.Auto {
			n++
		}
	}
	return n
}

// NewAutoLane builds a lane from [auto], or reports why it cannot be opened.
func (c *Config) NewAutoLane(name, about string) (*Lane, error) {
	switch {
	case c.Auto == nil:
		return nil, errors.New("auto lanes are off ([auto] unset)")
	case !laneName.MatchString(name):
		return nil, fmt.Errorf("bad lane name %q", name)
	case c.Lanes[name] != nil:
		return nil, fmt.Errorf("lane %s exists", name)
	case c.AutoLanes() >= c.Auto.MaxLanes:
		return nil, fmt.Errorf("auto lane cap %d reached", c.Auto.MaxLanes)
	}
	return &Lane{Name: name, Auto: true, About: about, Cwd: c.Auto.Cwd, Workspace: c.Auto.Workspace, Model: c.Auto.Model}, nil
}

// SaveAutoLane adds l to lanes.auto.toml at path.
func SaveAutoLane(path string, l *Lane) error {
	return editAuto(path, func(m map[string]*Lane) { m[l.Name] = l })
}

func editAuto(path string, f func(map[string]*Lane)) error {
	var file struct {
		Lanes map[string]*Lane `toml:"lane"`
	}
	if _, err := toml.DecodeFile(path, &file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if file.Lanes == nil {
		file.Lanes = map[string]*Lane{}
	}
	f(file.Lanes)
	var b bytes.Buffer
	b.WriteString("# Lanes bt dispatch opened because no lane in lanes.toml fitted. Edit or delete freely.\n")
	if err := toml.NewEncoder(&b).Encode(file); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// DeleteLane removes a lane from lanes.auto.toml when auto, else cuts its tables out of lanes.toml at
// path, leaving every other line (comments included) as it was.
func DeleteLane(path, name string, auto bool) error {
	if auto {
		found := false
		err := editAuto(AutoPath(path), func(m map[string]*Lane) { _, found = m[name]; delete(m, name) })
		if err == nil && !found {
			err = fmt.Errorf("no lane %s in %s", name, AutoPath(path))
		}
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	src, found := cutLane(string(b), name)
	if !found {
		return fmt.Errorf("no [lane.%s] table in %s", name, path)
	}
	c, err := ParseConfig(src)
	if err != nil {
		return fmt.Errorf("lanes.toml would not parse without lane %s: %w", name, err)
	}
	if c.Lanes[name] != nil {
		return fmt.Errorf("lane %s is defined outside its own table in %s", name, path)
	}
	return os.WriteFile(path, []byte(src), 0o644)
}

// cutLane drops the [lane.<name>] table and its sub-tables up to the next table header; comments
// right above that next header belong to it and stay.
func cutLane(src, name string) (string, bool) {
	var keep, held []string
	found, in := false, false
	for _, line := range strings.SplitAfter(src, "\n") {
		h := strings.TrimSpace(line)
		if m := tableHeader.FindStringSubmatch(h); m != nil {
			key := m[1]
			was := in
			in = key == "lane."+name || strings.HasPrefix(key, "lane."+name+".")
			found = found || in
			if was && !in {
				keep = append(keep, held...)
			}
			held = nil
		}
		switch {
		case !in:
			keep = append(keep, line)
		case strings.HasPrefix(h, "#"):
			held = append(held, line)
		case h != "":
			held = nil
		}
	}
	return strings.Join(keep, ""), found
}

func ParseConfig(src string) (*Config, error) {
	var c Config
	md, err := toml.Decode(src, &c)
	if err != nil {
		return nil, err
	}
	if extra := md.Undecoded(); len(extra) > 0 {
		keys := make([]string, len(extra))
		for i, k := range extra {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("unknown keys: %s", strings.Join(keys, ", "))
	}
	home, _ := os.UserHomeDir()
	if c.Lanes == nil {
		c.Lanes = map[string]*Lane{}
	}
	if c.Auto != nil {
		if rest, ok := strings.CutPrefix(c.Auto.Cwd, "~/"); ok {
			c.Auto.Cwd = filepath.Join(home, rest)
		}
	}
	for name, l := range c.Lanes {
		l.Name = name
		if rest, ok := strings.CutPrefix(l.Cwd, "~/"); ok {
			l.Cwd = filepath.Join(home, rest)
		}
	}
	return &c, nil
}

// Names returns lane names in a stable order.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Lanes))
	for n := range c.Lanes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
