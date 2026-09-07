// Package davd serves the aggregated occurrences as read-only CalDAV
// collections: one per person and one per configured view. It implements
// caldav.Backend over the store. Writes (PUT, DELETE, MKCALENDAR) are refused.
//
// sync-collection is not implemented; clients fall back to periodic
// calendar-query polling.
package davd

import (
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/bhaskell/Concordia/internal/icsout"
	"github.com/bhaskell/Concordia/internal/store"
	"github.com/bhaskell/Concordia/internal/views"
)

var errReadOnly = errors.New("davd: this CalDAV server is read-only")

// View is a named collection defined by a predicate over owner and tags.
type View struct {
	Name      string
	Predicate views.Predicate
}

// collection is one served CalDAV collection.
type collection struct {
	slug      string // URL path segment, e.g. "p-brandon" or "v-no-work"
	name      string // human-readable, for DAV:displayname
	owner     string // set for a person collection
	predicate views.Predicate
}

// Backend is a read-only caldav.Backend.
type Backend struct {
	store       *store.Store
	collections []collection
	bySlug      map[string]*collection
	opts        icsout.Options
	windowBack  time.Duration
	windowFwd   time.Duration
	prefix      string
	now         func() time.Time
}

// Options configures a Backend.
type Options struct {
	People        []string
	Views         []View
	SummaryPrefix bool
	WindowBack    time.Duration
	WindowFwd     time.Duration
	// Prefix is the URL path the CalDAV handler is mounted at, e.g. "/dav".
	Prefix string
	// Now overrides the clock, for tests.
	Now func() time.Time
}

// New builds a Backend.
func New(st *store.Store, o Options) *Backend {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	b := &Backend{
		store:      st,
		bySlug:     make(map[string]*collection),
		opts:       icsout.Options{SummaryPrefix: o.SummaryPrefix},
		windowBack: o.WindowBack,
		windowFwd:  o.WindowFwd,
		prefix:     strings.TrimSuffix(o.Prefix, "/"),
		now:        now,
	}
	for _, p := range o.People {
		b.collections = append(b.collections, collection{
			slug: "p-" + Slug(p), name: p + " (all)", owner: p,
		})
	}
	for _, v := range o.Views {
		b.collections = append(b.collections, collection{
			slug: "v-" + Slug(v.Name), name: "view: " + v.Name, predicate: v.Predicate,
		})
	}
	for i := range b.collections {
		b.bySlug[b.collections[i].slug] = &b.collections[i]
	}
	return b
}

// Handler returns the CalDAV HTTP handler.
func (b *Backend) Handler() http.Handler {
	return &caldav.Handler{Backend: b, Prefix: b.prefix}
}

// The library derives resource type from path depth under the prefix, so the
// hierarchy is fixed:
//
//	{prefix}/principal/                     user principal   (depth 1)
//	{prefix}/principal/cal/                 home set         (depth 2)
//	{prefix}/principal/cal/{slug}/          a calendar       (depth 3)
//	{prefix}/principal/cal/{slug}/{uid}.ics an object        (depth 4)
var homePrefix = []string{"principal", "cal"}

func (b *Backend) CurrentUserPrincipal(context.Context) (string, error) {
	return b.prefix + "/principal/", nil
}

func (b *Backend) CalendarHomeSetPath(context.Context) (string, error) {
	return b.prefix + "/principal/cal/", nil
}

func (b *Backend) collectionPath(slug string) string {
	return b.prefix + "/principal/cal/" + slug + "/"
}

// --- collections ---

func (b *Backend) ListCalendars(context.Context) ([]caldav.Calendar, error) {
	out := make([]caldav.Calendar, 0, len(b.collections))
	for i := range b.collections {
		out = append(out, b.calendar(&b.collections[i]))
	}
	return out, nil
}

func (b *Backend) GetCalendar(_ context.Context, urlPath string) (*caldav.Calendar, error) {
	c := b.resolve(urlPath)
	if c == nil {
		return nil, webdav.NewHTTPError(http.StatusNotFound, fmt.Errorf("davd: no collection at %s", urlPath))
	}
	cal := b.calendar(c)
	return &cal, nil
}

func (b *Backend) calendar(c *collection) caldav.Calendar {
	return caldav.Calendar{
		Path:                  b.collectionPath(c.slug),
		Name:                  c.name,
		SupportedComponentSet: []string{ical.CompEvent},
	}
}

// --- objects ---

func (b *Backend) ListCalendarObjects(ctx context.Context, urlPath string, _ *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	return b.objects(ctx, urlPath)
}

func (b *Backend) QueryCalendarObjects(ctx context.Context, urlPath string, q *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	objs, err := b.objects(ctx, urlPath)
	if err != nil {
		return nil, err
	}
	return caldav.Filter(q, objs)
}

func (b *Backend) GetCalendarObject(ctx context.Context, urlPath string, _ *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	resource := path.Base(urlPath)
	c := b.resolve(path.Dir(urlPath))
	if c == nil || !strings.HasSuffix(resource, ".ics") {
		return nil, webdav.NewHTTPError(http.StatusNotFound, fmt.Errorf("davd: no object at %s", urlPath))
	}
	objs, err := b.objectsIn(ctx, c)
	if err != nil {
		return nil, err
	}
	for i := range objs {
		if path.Base(objs[i].Path) == resource {
			return &objs[i], nil
		}
	}
	return nil, webdav.NewHTTPError(http.StatusNotFound, fmt.Errorf("davd: no object %s", resource))
}

func (b *Backend) objects(ctx context.Context, urlPath string) ([]caldav.CalendarObject, error) {
	c := b.resolve(urlPath)
	if c == nil {
		return nil, webdav.NewHTTPError(http.StatusNotFound, fmt.Errorf("davd: no collection at %s", urlPath))
	}
	return b.objectsIn(ctx, c)
}

func (b *Backend) objectsIn(ctx context.Context, c *collection) ([]caldav.CalendarObject, error) {
	now := b.now()
	query := store.OccurrenceQuery{
		From:  now.Add(-b.windowBack),
		To:    now.Add(b.windowFwd),
		Owner: c.owner,
	}
	rows, err := b.store.Occurrences(ctx, query)
	if err != nil {
		return nil, err
	}

	collPath := b.collectionPath(c.slug)
	out := make([]caldav.CalendarObject, 0, len(rows))
	for _, row := range rows {
		if c.predicate != nil && !c.predicate(views.Subject{Owner: row.Owner, Tags: row.Tags}) {
			continue
		}
		out = append(out, b.object(collPath, row))
	}
	return out, nil
}

// resolve maps a collection or resource path to its collection, or nil.
func (b *Backend) resolve(urlPath string) *collection {
	rest := strings.Trim(strings.TrimPrefix(path.Clean(urlPath), b.prefix), "/")
	parts := strings.Split(rest, "/")
	if len(parts) <= len(homePrefix) {
		return nil
	}
	for i, seg := range homePrefix {
		if parts[i] != seg {
			return nil
		}
	}
	return b.bySlug[parts[len(homePrefix)]]
}

func (b *Backend) object(collection string, row store.OccurrenceRow) caldav.CalendarObject {
	uid := icsout.OccurrenceUID(row.EventUID, row.Start)
	cal := icsout.Calendar([]icsout.Occurrence{{
		UID:          uid,
		Owner:        row.Owner,
		CalendarName: row.CalendarName,
		Summary:      row.Summary,
		Location:     row.Location,
		Start:        row.Start,
		End:          row.End,
		AllDay:       row.AllDay,
		Status:       row.Status,
		Tags:         row.Tags,
		LastModified: row.LastModified,
	}}, b.opts)

	var buf bytes.Buffer
	_ = icsout.Encode(&buf, cal)

	return caldav.CalendarObject{
		Path:          collection + uid + ".ics",
		ModTime:       row.LastModified,
		ContentLength: int64(buf.Len()),
		ETag:          etag(buf.Bytes()),
		Data:          cal,
	}
}

// --- writes: refused ---

func (b *Backend) CreateCalendar(context.Context, *caldav.Calendar) error {
	return webdav.NewHTTPError(http.StatusForbidden, errReadOnly)
}

func (b *Backend) PutCalendarObject(context.Context, string, *ical.Calendar, *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	return nil, webdav.NewHTTPError(http.StatusForbidden, errReadOnly)
}

func (b *Backend) DeleteCalendarObject(context.Context, string) error {
	return webdav.NewHTTPError(http.StatusForbidden, errReadOnly)
}

// slug reduces s to lowercase ASCII alphanumerics and single hyphens.
// Slug reduces s to lowercase ASCII alphanumerics and single hyphens for use
// as a URL path segment.
func Slug(s string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			hyphen = false
		default:
			if b.Len() > 0 && !hyphen {
				b.WriteByte('-')
				hyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func etag(b []byte) string {
	sum := sha1.Sum(b)
	return fmt.Sprintf(`"%x"`, sum)
}
