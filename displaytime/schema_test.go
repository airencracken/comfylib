// SPDX-License-Identifier: AGPL-3.0-or-later
package displaytime

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTimezoneSchemaRejectsAdversarialRows(t *testing.T) {
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `import sqlite3,json,sys
c=sqlite3.connect(':memory:')
for statement in json.load(sys.stdin):c.execute(statement)
c.execute("INSERT INTO comfy_display_timezones VALUES(1,'Asia/Kolkata')")
assert c.execute('SELECT timezone FROM comfy_display_timezones WHERE member=1').fetchone()[0]=='Asia/Kolkata'
for member,zone in [(0,'UTC'),(-1,'UTC'),(2,''),(2,'Local'),(2,'../UTC'),(2,'/etc/passwd'),(2,'UTC\x00'),(2,'America/New_York\n'),(2,'X'*65)]:
 try:c.execute('INSERT INTO comfy_display_timezones VALUES(?,?)',(member,zone))
 except sqlite3.IntegrityError:continue
 raise AssertionError('schema accepted '+repr((member,zone)))
`)
	cmd.Stdin = strings.NewReader(string(data))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual SQLite schema: %v\n%s", err, out)
	}
}
