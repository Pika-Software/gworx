package ui

import (
	"strings"
	"sync"
	"unicode"

	"github.com/Pika-Software/gworx/internal/server"
)

const maxSuggestions = 6

// suggestCommands completes a console line from sent history, a built-in
// catalog, and names learned from server output.
// srcds reads stdin a line at a time and does not send its own tab completion
// back, so suggestions stay on the client. Names the server has already
// printed (cvarlist, find, "name" = "value") are learned as they scroll by.
func suggestCommands(line string, history, learned []string) []string {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(s string) bool {
		if s == "" || s == line || seen[s] {
			return len(out) >= maxSuggestions
		}
		seen[s] = true
		out = append(out, s)
		return len(out) >= maxSuggestions
	}
	for i := len(history) - 1; i >= 0; i-- {
		if hasPrefixFold(history[i], line) && add(history[i]) {
			return out
		}
	}

	names, hints := consoleCatalog()
	cmd, arg, inArg := splitConsoleArg(line)
	if !inArg {
		for _, name := range filterPrefix(append(append([]string{}, names...), learned...), line) {
			if add(name) {
				return out
			}
		}
		return out
	}

	canon := cmd
	var args []string
	for k, v := range hints {
		if strings.EqualFold(k, cmd) {
			canon = k
			args = v
			break
		}
	}
	for _, a := range args {
		if arg == "" || hasPrefixFold(a, arg) {
			if add(canon + " " + a) {
				return out
			}
		}
	}
	return out
}

func filterPrefix(names []string, prefix string) []string {
	var match []string
	seen := map[string]bool{}
	for _, name := range names {
		key := strings.ToLower(name)
		if name == "" || seen[key] || !hasPrefixFold(name, prefix) {
			continue
		}
		seen[key] = true
		match = append(match, name)
	}
	return match
}

func hasPrefixFold(s, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix))
}

// splitConsoleArg reports the command and the argument token being typed.
// inArg is false once a second argument has been started; those lines only
// match whole history entries.
func splitConsoleArg(line string) (cmd, arg string, inArg bool) {
	trimmed := strings.TrimLeft(line, " ")
	i := strings.IndexByte(trimmed, ' ')
	if i < 0 {
		return trimmed, "", false
	}
	cmd = trimmed[:i]
	rest := trimmed[i+1:]
	fields := strings.Fields(rest)
	switch len(fields) {
	case 0:
		return cmd, "", true
	case 1:
		return cmd, fields[0], true
	default:
		return cmd, "", false
	}
}

// parseCvarLine returns a console name printed by the dedicated server.
// It accepts cvarlist rows (`name : value : flags`) and assignments (`"name" = "value"`).
func parseCvarLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if strings.HasPrefix(line, `"`) {
		rest := line[1:]
		i := strings.IndexByte(rest, '"')
		if i <= 0 {
			return ""
		}
		name := rest[:i]
		if !strings.Contains(rest[i:], "=") || !validConsoleName(name) {
			return ""
		}
		return name
	}
	parts := strings.SplitN(line, ":", 3)
	if len(parts) < 3 {
		return ""
	}
	name := strings.TrimSpace(parts[0])
	if !learnableConsoleName(name) {
		return ""
	}
	return name
}

func validConsoleName(name string) bool {
	if len(name) < 2 || len(name) > 64 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// learnableConsoleName rejects ordinary log text that happens to contain colons.
// Cvars almost always include '_'; a bare word has to be a lowercase command.
func learnableConsoleName(name string) bool {
	if !validConsoleName(name) {
		return false
	}
	if strings.Contains(name, "_") {
		return true
	}
	if len(name) < 3 {
		return false
	}
	for _, r := range name {
		if unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

var (
	catalogOnce  sync.Once
	catalogNames []string
	catalogHints map[string][]string
)

func consoleCatalog() ([]string, map[string][]string) {
	catalogOnce.Do(buildConsoleCatalog)
	return catalogNames, catalogHints
}

func buildConsoleCatalog() {
	zeroOne := []string{"0", "1"}
	catalogNames = []string{
		"status", "stats", "ping", "quit", "exit",
		"maps", "map", "changelevel", "gamemode", "hostname",
		"kick", "kickid", "ban", "banid", "unban", "listid", "removeid", "writeid", "writeip", "removeip",
		"users", "say", "exec", "find", "help", "cvarlist", "differences", "path", "version", "clear", "echo",
		"alias", "bind", "toggle", "incrementvar", "condump", "developer",
		"sv_cheats", "sv_password", "sv_gravity", "sv_friction", "sv_accelerate", "sv_airaccelerate",
		"sv_maxvelocity", "sv_noclipspeed", "sv_lan", "sv_region", "sv_voiceenable", "sv_alltalk",
		"sv_pure", "sv_allowcslua", "sv_allowupload", "sv_allowdownload", "sv_downloadurl", "sv_loadingurl",
		"sv_timeout", "sv_maxrate", "sv_minrate", "sv_maxupdaterate", "sv_minupdaterate",
		"sv_maxcmdrate", "sv_mincmdrate", "sv_hibernate_think", "sv_logecho", "sv_logfile", "sv_logbans",
		"sv_rcon_banpenalty", "sv_setsteamaccount", "rcon_password",
		"host_timescale", "host_framerate", "host_workshop_collection",
		"net_maxfilesize", "ip", "hostport", "maxplayers",
		"lua_run", "lua_openscript", "lua_find", "lua_dumptimers",
		"ent_create", "ent_remove", "ent_fire",
		"log", "logaddress_add", "logaddress_del", "logaddress_list",
		"tv_enable", "tv_delay", "tv_name", "tv_maxclients", "tv_password", "tv_autorecord",
	}
	catalogHints = map[string][]string{
		"changelevel": append([]string(nil), server.Maps...),
		"map":         append([]string(nil), server.Maps...),
		"gamemode":    append([]string(nil), server.Gamemodes...),
		"developer":   {"0", "1", "2"},
	}
	for _, name := range []string{
		"sv_cheats", "sv_lan", "sv_voiceenable", "sv_alltalk", "sv_allowcslua",
		"sv_allowupload", "sv_allowdownload", "sv_logecho", "sv_logfile", "sv_logbans",
		"sv_hibernate_think", "tv_enable", "tv_autorecord",
	} {
		catalogHints[name] = zeroOne
	}
}
