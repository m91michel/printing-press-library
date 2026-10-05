// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source computed

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

func init() {
	registerNovelCommand(func(root *cobra.Command, flags *rootFlags) {
		addNovelCommandIfAbsent(root, newCPVCmd(flags))
	})
}

// cpvDescription returns the label of the most specific reference entry that
// prefixes the code, so 45233120 resolves to "Construction work for pipelines...".
func cpvDescription(code string) string {
	code = ted.NormalizeCPV(code)
	best, bestLen := "", 0
	for _, e := range cpvReference {
		p := ted.CPVPrefix(e.Code)
		if strings.HasPrefix(code, p) && len(p) > bestLen {
			best, bestLen = e.Description, len(p)
		}
	}
	return best
}

func newCPVCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cpv",
		Short: "Look up Common Procurement Vocabulary (CPV) codes and descriptions",
		Annotations: map[string]string{
			"pp:parent-group": "true",
		},
		RunE: parentNoSubcommandRunE(flags),
	}
	cmd.AddCommand(newCPVGetCmd(flags), newCPVSearchCmd(flags))
	return cmd
}

func newCPVGetCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "get [code]",
		Short:   "Show the description of a CPV code or prefix",
		Example: "  eu-tenders-pp-cli cpv get 45500000\n  eu-tenders-pp-cli cpv get 4523 --json",
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "computed",
			"pp:happy-args":  "code=45500000",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "cpv get")
			}
			if len(args) == 0 {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("a CPV code is required, e.g. cpv get 45000000"))
			}
			code := ted.NormalizeCPV(args[0])
			desc := cpvDescription(code)
			if desc == "" {
				return notFoundErr(fmt.Errorf("no CPV reference entry covers %s; try cpv search <keyword>", code))
			}
			entry := cpvEntry{Code: code, Description: desc}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), entry, flags)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", entry.Code, entry.Description)
			return nil
		},
	}
	return cmd
}

func newCPVSearchCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "search [keyword]",
		Short:   "Find CPV codes whose description contains a keyword",
		Example: "  eu-tenders-pp-cli cpv search machinery\n  eu-tenders-pp-cli cpv search construction --json",
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "computed",
			"pp:happy-args":  "keyword=construction",
			// A keyword with no match is an empty search result, exit 0.
			"pp:no-error-path-probe": "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "cpv search")
			}
			if len(args) == 0 {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("a keyword is required, e.g. cpv search machinery"))
			}
			term := strings.ToLower(strings.Join(args, " "))
			out := make([]cpvEntry, 0)
			for _, e := range cpvReference {
				if strings.Contains(strings.ToLower(e.Description), term) || strings.HasPrefix(e.Code, term) {
					out = append(out, e)
				}
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), out, flags)
			}
			if len(out) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No CPV entries match %q.\n", term)
				return nil
			}
			tw := newTabWriter(cmd.OutOrStdout())
			for _, e := range out {
				fmt.Fprintf(tw, "%s\t%s\n", e.Code, e.Description)
			}
			return tw.Flush()
		},
	}
	return cmd
}
