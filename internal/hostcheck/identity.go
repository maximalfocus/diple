package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// identityField reads one field of a `name=<agent> state=<state>` identity.
func identityField(identity, key string) string {
	for _, f := range strings.Fields(identity) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	return ""
}

// herdrPaneMain reads `herdr agent list` from stdin and prints what herdr
// calls one pane, and its state, as an identity line. It prints nothing when
// herdr lists no agent for the pane, which the host check then reports.
func herdrPaneMain(args []string) int {
	fs := flag.NewFlagSet("herdr-pane", flag.ContinueOnError)
	pane := fs.String("pane", "", "the pane id herdr gave the wrapped session")
	name := fs.String("name", "",
		"the agent name the wrapped session was started under, for herdr 0.7")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if line := herdrIdentity(data, *pane, *name); line != "" {
		fmt.Println(line)
	}
	return 0
}

// herdrIdentity finds the pane in herdr's agent list, by pane id or by the
// name the session was started under.
func herdrIdentity(data []byte, pane, name string) string {
	var list struct {
		Result struct {
			Agents []map[string]any `json:"agents"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &list) != nil {
		return ""
	}
	for _, a := range list.Result.Agents {
		str := func(k string) string { s, _ := a[k].(string); return s }
		if (pane != "" && str("pane_id") == pane) || (name != "" && str("name") == name) {
			return fmt.Sprintf("name=%s state=%s", str("agent"), str("agent_status"))
		}
	}
	return ""
}
