// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"sort"
	"strings"
)

func init() {
	register("msi-inspect", "Read MSI properties, directories and file tables without installation", "properties? dirs? files", inspectMSI)
}
func inspectMSI(a *app, o options) error {
	if len(o.args) != 1 {
		return fmt.Errorf("expected PACKAGE.msi")
	}
	database, e := openMSI(o.args[0])
	if e != nil {
		return e
	}
	defer closeMSI(database)
	rows, e := queryMSI(database, "SELECT `Property`, `Value` FROM `Property`")
	if e != nil {
		return e
	}
	properties := map[string]string{}
	for _, row := range rows {
		if len(row) >= 2 {
			properties[row[0]] = row[1]
		}
	}
	for _, key := range []string{"ProductName", "ProductVersion", "ProductCode", "UpgradeCode", "Manufacturer", "ALLUSERS", "MSIINSTALLPERUSER", "INSTALLFOLDER", "OFFICIALGIT", "REPLACEGIT"} {
		if value, ok := properties[key]; ok {
			fmt.Fprintf(a.out, "%-20s %s\n", key, value)
		}
	}
	for _, table := range []string{"File", "Component", "Directory", "Registry", "Shortcut", "CustomAction", "InstallExecuteSequence"} {
		rows, e := queryMSI(database, "SELECT * FROM `"+table+"`")
		if e != nil {
			return e
		}
		fmt.Fprintf(a.out, "%s=%d ", table, len(rows))
	}
	fmt.Fprintln(a.out)
	if o.yes("properties") {
		keys := make([]string, 0, len(properties))
		for key := range properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(a.out, "%-32s %s\n", key, properties[key])
		}
	}
	if o.yes("dirs") {
		rows, e := queryMSI(database, "SELECT `Directory`, `Directory_Parent`, `DefaultDir` FROM `Directory` ORDER BY `Directory`")
		if e != nil {
			return e
		}
		for _, row := range rows {
			fmt.Fprintln(a.out, "  "+strings.Join(row, " | "))
		}
	}
	n, e := o.integer("files", 0)
	if e != nil || n < 0 {
		return fmt.Errorf("files must be nonnegative")
	}
	if n > 0 {
		rows, e := queryMSI(database, "SELECT `File`, `Component_`, `FileName`, `FileSize` FROM `File` ORDER BY `File`")
		if e != nil {
			return e
		}
		if n > len(rows) {
			n = len(rows)
		}
		for _, row := range rows[:n] {
			fmt.Fprintln(a.out, "  "+strings.Join(row, " | "))
		}
	}
	return nil
}
