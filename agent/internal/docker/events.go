package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Event is a change to a container of a Compose project.
type Event struct {
	Action  string // e.g. start, die, destroy, "health_status: healthy"
	Project string
	Service string
}

// relevantActions are the container events that change what gantry shows.
// Others (exec_*, attach, top, ...) don't.
var relevantActions = map[string]bool{
	"create": true, "start": true, "restart": true, "die": true, "stop": true,
	"destroy": true, "pause": true, "unpause": true, "rename": true, "oom": true,
}

func relevant(action string) bool {
	return relevantActions[action] || strings.HasPrefix(action, "health_status")
}

// Events follows the container events of Compose projects and calls fn for
// each relevant one, until ctx ends or the stream breaks (e.g. Docker
// restarts). connected is called once Docker has accepted the request, so
// events from then on are not missed. It returns ctx.Err() when ctx ended.
func (c *Client) Events(ctx context.Context, connected func(), fn func(Event)) error {
	filters := `{"type":["container"],"label":["` + LabelProject + `"]}`
	u := "http://docker/events?filters=" + url.QueryEscape(filters)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("events: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError("events", resp)
	}
	connected()

	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Type   string
			Action string
			Actor  struct {
				Attributes map[string]string
			}
		}
		if err := dec.Decode(&msg); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return errors.New("events: stream ended")
			}
			return fmt.Errorf("events: %w", err)
		}
		project := msg.Actor.Attributes[LabelProject]
		if msg.Type != "container" || project == "" || !relevant(msg.Action) {
			continue
		}
		fn(Event{Action: msg.Action, Project: project, Service: msg.Actor.Attributes[LabelService]})
	}
}
