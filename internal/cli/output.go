package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"airs/internal/api"
)

func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if r, err := filepath.EvalSymlinks(home); err == nil {
		if q := tildeUnder(p, r); q != p {
			return q
		}
	}
	return tildeUnder(p, home)
}

func tildeUnder(p, home string) string {
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

func statusText(s string) string {
	if s == api.StatusBlocked {
		return "blocked (untrusted network)"
	}
	return s
}

func printList(w io.Writer, items []api.ItemView) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tPATH\tURL\tSTATUS")
	for _, it := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", strconv.Itoa(it.ID), it.Name, tildePath(it.Path), it.URL, statusText(it.Status))
	}
	tw.Flush()
}

func printUntrusted(w io.Writer, it api.ItemView, typed string) {
	fmt.Fprintf(w, "⚠ This network isn't trusted, so %s is only visible on this Mac.\n", it.Name)
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "  airs trust\ttrust this network (e.g. your home Wi-Fi)\n")
	fmt.Fprintf(tw, "  airs %s --any\tshare this %s on any network\n", typed, map[bool]string{true: "folder", false: "file"}[it.IsDir])
	tw.Flush()
}
