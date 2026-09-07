package httpd

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/bhaskell/Concordia/internal/store"
	"github.com/bhaskell/Concordia/internal/views"
)

// filter is the web view's query state.
type filter struct {
	Owner string
	Tag   string
	View  string
	Days  int
}

const defaultDays = 14

func parseFilter(q url.Values) filter {
	f := filter{
		Owner: q.Get("owner"),
		Tag:   q.Get("tag"),
		View:  q.Get("view"),
		Days:  defaultDays,
	}
	if d := q.Get("days"); d != "" {
		if n, err := parsePositiveInt(d); err == nil && n <= 400 {
			f.Days = n
		}
	}
	return f
}

// query renders f as a "?..." string, or "" when it is all defaults.
func (f filter) query() string {
	v := url.Values{}
	if f.Owner != "" {
		v.Set("owner", f.Owner)
	}
	if f.Tag != "" {
		v.Set("tag", f.Tag)
	}
	if f.View != "" {
		v.Set("view", f.View)
	}
	if f.Days != defaultDays {
		v.Set("days", fmt.Sprint(f.Days))
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// toggled returns f with param set to value, or param cleared if it already
// holds value.
func (f filter) toggled(param, value string) filter {
	set := func(cur *string) {
		if *cur == value {
			*cur = ""
		} else {
			*cur = value
		}
	}
	switch param {
	case "owner":
		set(&f.Owner)
	case "tag":
		set(&f.Tag)
	case "view":
		set(&f.View)
	}
	return f
}

// --- template data ---

type pageData struct {
	Title  string
	Chips  chipSet
	Agenda agendaData
}

type chipSet struct {
	Owners []chip
	Views  []chip
	Tags   []chip
}

type chip struct {
	Label  string
	Query  string
	Active bool
	Class  string
}

type agendaData struct {
	Days []dayGroup
}

type dayGroup struct {
	Label  string
	Today  bool
	Events []eventRow
}

type eventRow struct {
	Time       string
	Summary    string
	Owner      string
	OwnerClass string
	Calendar   string
	Tags       []string
	Tentative  bool
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request)    { s.renderWeb(w, r, "layout") }
func (s *Server) handleViewFrag(w http.ResponseWriter, r *http.Request) { s.renderWeb(w, r, "view") }

func (s *Server) renderWeb(w http.ResponseWriter, r *http.Request, tmpl string) {
	f := parseFilter(r.URL.Query())
	loc := s.displayLoc()
	now := s.now().In(loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	to := from.AddDate(0, 0, f.Days)

	rows, err := s.cfg.Store.Occurrences(r.Context(), store.OccurrenceQuery{
		From:  from,
		To:    to,
		Owner: f.Owner,
	})
	if err != nil {
		s.log.Error("web query failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var pred views.Predicate
	if f.View != "" {
		pred = s.webViewPredicate(f.View)
	}

	tagSet := map[string]struct{}{}
	var shown []store.OccurrenceRow
	for _, row := range rows {
		for _, t := range row.Tags {
			tagSet[t] = struct{}{}
		}
		if f.Tag != "" && !hasTag(row.Tags, f.Tag) {
			continue
		}
		if pred != nil && !pred(views.Subject{Owner: row.Owner, Tags: row.Tags}) {
			continue
		}
		shown = append(shown, row)
	}

	data := pageData{
		Title:  "Concordia",
		Chips:  s.buildChips(f, tagSet),
		Agenda: agendaData{Days: groupByDay(shown, loc, now)},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, tmpl, data); err != nil {
		s.log.Error("web render failed", "err", err)
	}
}

func (s *Server) buildChips(f filter, tagSet map[string]struct{}) chipSet {
	var cs chipSet
	for _, feed := range s.cfg.Feeds {
		switch {
		case feed.Owner != "":
			cs.Owners = append(cs.Owners, chip{
				Label:  feed.Owner,
				Query:  f.toggled("owner", feed.Owner).query(),
				Active: f.Owner == feed.Owner,
				Class:  ownerClass(feed.Owner),
			})
		case feed.Predicate != nil:
			cs.Views = append(cs.Views, chip{
				Label:  feed.Name,
				Query:  f.toggled("view", feed.Name).query(),
				Active: f.View == feed.Name,
			})
		}
	}
	tags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	for _, t := range tags {
		cs.Tags = append(cs.Tags, chip{
			Label:  t,
			Query:  f.toggled("tag", t).query(),
			Active: f.Tag == t,
		})
	}
	return cs
}

func (s *Server) webViewPredicate(name string) views.Predicate {
	for _, feed := range s.cfg.Feeds {
		if feed.Owner == "" && feed.Predicate != nil && feed.Name == name {
			return feed.Predicate
		}
	}
	return nil
}

func groupByDay(rows []store.OccurrenceRow, loc *time.Location, now time.Time) []dayGroup {
	today := dateKey(now)
	tomorrow := dateKey(now.AddDate(0, 0, 1))

	var days []dayGroup
	var cur *dayGroup
	var curKey string
	for _, row := range rows {
		local := row.Start.In(loc)
		key := dateKey(local)
		if cur == nil || key != curKey {
			label := local.Format("Mon Jan 2")
			switch key {
			case today:
				label = "Today"
			case tomorrow:
				label = "Tomorrow"
			}
			days = append(days, dayGroup{Label: label, Today: key == today})
			cur = &days[len(days)-1]
			curKey = key
		}
		cur.Events = append(cur.Events, eventRow{
			Time:       eventTime(row, loc),
			Summary:    row.Summary,
			Owner:      row.Owner,
			OwnerClass: ownerClass(row.Owner),
			Calendar:   row.CalendarName,
			Tags:       row.Tags,
			Tentative:  row.Status == "tentative",
		})
	}
	return days
}

func eventTime(row store.OccurrenceRow, loc *time.Location) string {
	if row.AllDay {
		return "all day"
	}
	return row.Start.In(loc).Format("15:04")
}

func dateKey(t time.Time) string { return t.Format("2006-01-02") }

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// ownerClass maps an owner name to a stable CSS palette class.
func ownerClass(owner string) string {
	if owner == "" {
		return ""
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(owner))
	return fmt.Sprintf("owner-%d", h.Sum32()%8)
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 {
			return 0, fmt.Errorf("too large")
		}
	}
	if n == 0 {
		return 0, fmt.Errorf("zero")
	}
	return n, nil
}
