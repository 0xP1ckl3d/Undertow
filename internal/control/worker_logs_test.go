package control

import (
	"fmt"
	"strings"
	"testing"
)

func TestWorkerLogsBoundedReplayAndCursor(t *testing.T) {
	logs:=NewWorkerLogBuffer()
	changes:=0
	logs.OnWrite(func(){changes++})
	for i:=0;i<1050;i++ { _,_ = fmt.Fprintf(logs,"entry %d\n",i) }
	if changes!=1050 { t.Fatalf("published %d changes",changes) }
	initial:=logs.Snapshot(0)
	if len(initial.Entries)!=40 || initial.Entries[0].Seq!=1011 || initial.Next!=1050 { t.Fatalf("unexpected initial snapshot: %+v",initial) }
	gap:=logs.Snapshot(1)
	if !gap.Gap || len(gap.Entries)!=100 || gap.Entries[0].Seq!=51 || gap.Next!=150 { t.Fatalf("unexpected replay gap: %+v",gap) }
	if next:=logs.Snapshot(gap.Next); next.Gap || next.Entries[0].Seq!=151 { t.Fatalf("unexpected next page: %+v",next) }
	long:=strings.Repeat("a",5000)
	_,_=logs.Write([]byte(long+"\n"))
	item:=logs.Snapshot(1050)
	if len(item.Entries)!=1 || len(item.Entries[0].Text)!=4096 { t.Fatalf("long log line not bounded: %+v",item) }
}
