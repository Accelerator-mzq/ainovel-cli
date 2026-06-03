package flow

import "testing"

// TestDedupeKey_BatchVsSingle 验证批量指令与单派指令、以及不同批次之间的去重键行为。
func TestDedupeKey_BatchVsSingle(t *testing.T) {
	single := &Instruction{Agent: "writer_wuzei", Task: "写第 1 章候选稿"}
	batchA := &Instruction{Batch: []SubTask{
		{Agent: "writer_wuzei", Task: "写第 1 章候选稿"},
		{Agent: "writer_tudou", Task: "写第 1 章候选稿"},
	}}
	batchB := &Instruction{Batch: []SubTask{ // 少了 tudou
		{Agent: "writer_wuzei", Task: "写第 1 章候选稿"},
	}}
	if dedupeKey(single) == dedupeKey(batchA) {
		t.Fatal("单派与批量键不应相同")
	}
	if dedupeKey(batchA) == dedupeKey(batchB) {
		t.Fatal("不同 pending 集合的批量键应不同（更小批不能被误杀）")
	}
	if dedupeKey(batchA) != dedupeKey(&Instruction{Batch: batchA.Batch}) {
		t.Fatal("相同批量键应相等（重复派发应去重）")
	}
}

// TestFailedFromLastBatch 验证"上一批派出但仍缺候选且未弃权者计为失败"的逻辑。
// failedFromLastBatch：上一批中仍缺候选且未弃权者计为失败。
func TestFailedFromLastBatch(t *testing.T) {
	state := State{
		CandidatesReady: map[string]bool{"wuzei": true, "tudou": false, "maibao": false},
		Abandoned:       map[string]bool{"maibao": true},
	}
	got := failedFromLastBatch([]string{"wuzei", "tudou", "maibao"}, state)
	// wuzei 已就绪→排除；maibao 已弃权→排除；只剩 tudou。
	if len(got) != 1 || got[0] != "tudou" {
		t.Fatalf("应只判定 tudou 失败, got %v", got)
	}
}
