package fold

import "testing"

// step records which ordered test ran last; each passes only when every
// one before it ran first, in declaration order.
var step int

func TestOrder1(t *testing.T) {
	if step != 0 {
		t.Fatalf("ran after step %d", step)
	}
	step = 1
}

func TestOrder2(t *testing.T) {
	if step != 1 {
		t.Fatalf("ran after step %d", step)
	}
	step = 2
}

func TestOrder3(t *testing.T) {
	if step != 2 {
		t.Fatalf("ran after step %d", step)
	}
	step = 3
}

func TestOrder4(t *testing.T) {
	if step != 3 {
		t.Fatalf("ran after step %d", step)
	}
	step = 4
}

func TestOrder5(t *testing.T) {
	if step != 4 {
		t.Fatalf("ran after step %d", step)
	}
	step = 5
}

func TestOrder6(t *testing.T) {
	if step != 5 {
		t.Fatalf("ran after step %d", step)
	}
	step = 6
}

func TestOrder7(t *testing.T) {
	if step != 6 {
		t.Fatalf("ran after step %d", step)
	}
	step = 7
}

func TestOrder8(t *testing.T) {
	if step != 7 {
		t.Fatalf("ran after step %d", step)
	}
	step = 8
}
