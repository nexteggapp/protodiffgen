package apply

// run is a stretch of a sequence diff between two kept elements: the base
// elements removed and the new elements added there. next is the base index
// of the kept element that follows, or n.
type run struct {
	removed []int
	added   []int
	next    int
}

// myers diffs a[0:n] against b[0:m] with Myers' greedy algorithm and returns
// the runs of change between kept elements. It takes O((n+m)·d) time and
// O(d²) memory for an edit distance of d.
func myers(n, m int, eq func(i, j int) bool) []run {
	max := n + m
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int // trace[d][k+d] is v[k] before step d+1
	var x, y int
	for d := 0; d <= max; d++ {
		for k := -d; k <= d; k += 2 {
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y = x - k
			for x < n && y < m && eq(x, y) {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				snap := make([]int, 2*d+1)
				copy(snap, v[off-d:off+d+1])
				trace = append(trace, snap)
				return runs(backtrack(trace, n, m), n)
			}
		}
		snap := make([]int, 2*d+1)
		copy(snap, v[off-d:off+d+1])
		trace = append(trace, snap)
	}
	panic("unreachable")
}

type step struct {
	kind byte // 'k' keep, 'd' delete a[i], 'i' insert b[j]
	i, j int
}

func backtrack(trace [][]int, n, m int) []step {
	var out []step
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		k := x - y
		var prevK int
		if d == 0 {
			prevK = 0
		} else {
			prev := trace[d-1]
			at := func(k int) int { return prev[k+d-1] }
			if k == -d || (k != d && at(k-1) < at(k+1)) {
				prevK = k + 1
			} else {
				prevK = k - 1
			}
		}
		var prevX int
		if d > 0 {
			prevX = trace[d-1][prevK+d-1]
		}
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			out = append(out, step{'k', x, y})
		}
		if d > 0 {
			if x == prevX {
				y--
				out = append(out, step{'i', x, y})
			} else {
				x--
				out = append(out, step{'d', x, y})
			}
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func runs(steps []step, n int) []run {
	var out []run
	var cur run
	flush := func(next int) {
		if len(cur.removed) > 0 || len(cur.added) > 0 {
			cur.next = next
			out = append(out, cur)
		}
		cur = run{}
	}
	for _, s := range steps {
		switch s.kind {
		case 'k':
			flush(s.i)
		case 'd':
			cur.removed = append(cur.removed, s.i)
		case 'i':
			cur.added = append(cur.added, s.j)
		}
	}
	flush(n)
	return out
}
