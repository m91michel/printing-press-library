// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// The root help renders the slug-derived "Eu Tenders"; the brand is "EU Tenders".
// The generated notices example lists every one of TED's ~1,800 field names;
// a short real query reads better and gives verify/dogfood a working fixture.
// The generated import command posts JSONL to create endpoints, which the
// read-only TED search API does not have, so it stays out of help and MCP.
func init() {
	registerNovelCommand(func(root *cobra.Command, flags *rootFlags) {
		root.Short = strings.Replace(root.Short, "Eu Tenders CLI", "EU Tenders CLI", 1)
		root.Long = strings.Replace(root.Long, "Eu Tenders CLI", "EU Tenders CLI", 1)
		if notices, _, err := root.Find([]string{"notices"}); err == nil && notices != root {
			notices.Short = "Search TED notices with an expert query (live API)"
			notices.Long = "Search TED notices with the TED expert-query language and choose the returned fields.\n" +
				"Run 'eu-tenders-pp-cli fields --contains <text>' for field names; queries accept SORT BY publication-date DESC."
			notices.Example = strings.Trim(`
  eu-tenders-pp-cli notices --query "buyer-country=DEU AND notice-type=can-standard SORT BY publication-date DESC" --fields publication-number,winner-name,buyer-name --limit 10
  eu-tenders-pp-cli notices --query "classification-cpv=45500000" --fields publication-number,title-proc --limit 5 --json`, "\n")
			if notices.Annotations == nil {
				notices.Annotations = map[string]string{}
			}
			notices.Annotations["pp:happy-args"] = "--query=buyer-country=LUX SORT BY publication-date DESC;--fields=publication-number,notice-type;--limit=3"
		}
		if imp, _, err := root.Find([]string{"import"}); err == nil && imp != root {
			imp.Hidden = true
			if imp.Annotations == nil {
				imp.Annotations = map[string]string{}
			}
			imp.Annotations["mcp:hidden"] = "true"
		}
	})
}
