package resolver

import "github.com/liuymcn/flash-code-graph/internal/model"

// ComputeMRO computes Method Resolution Order for a class in an inheritance hierarchy.
//
// For single inheritance (Java, Go, most TS), returns a simple linear chain by
// recursively prepending each class to its parent's MRO:
//
//	GrandChild → Child → Base  ⟹  [GrandChild, Child, Base]
//
// For multiple inheritance (Python), uses C3 linearization to produce a
// deterministic, monotonic ordering that respects both local precedence
// (left-to-right parent order) and the constraint that a class always appears
// before its parents. The algorithm:
//
//  1. Recursively compute MRO for each parent.
//  2. Merge the parent MROs plus the direct parent list using c3Merge:
//     a. Scan heads (first element) of all remaining sequences.
//     b. Pick the first head that does NOT appear in the tail of any sequence.
//     c. Append it to the result and remove it from all sequences.
//     d. Repeat until all sequences are empty.
//  3. If no valid head is found, the hierarchy is inconsistent (e.g., cyclic).
//
// Example — diamond inheritance D(B, C), B(A), C(A):
//
//	MRO(B) = [B, A]
//	MRO(C) = [C, A]
//	merge([B, A], [C, A], [B, C])
//	  step 1: B is head of seq0, not in tail of seq1([A]) or seq2([C]) → pick B
//	          after removal: seq0=[A], seq1=[C,A], seq2=[C]
//	  step 2: A is head of seq0, but A is in tail of seq1([A]) → skip
//	          C is head of seq1=[C,A]; check tails: seq0=[A]→tail=[], seq2=[C]→tail=[] → pick C
//	          (C appears as head of seq2, not in any tail, so no conflict)
//	          after removal: seq0=[A], seq1=[A], seq2=[]
//	  step 3: A is head, not in any tail → pick A
//	Result: [D, B, C, A]
func (resolver *Resolver) ComputeMRO(className string, heritage []model.RawHeritage) []string {
	parentMap := buildParentMap(heritage)
	parents := parentMap[className]

	if len(parents) == 0 {
		return []string{className}
	}

	if len(parents) == 1 {
		// Single inheritance: linear chain
		return append([]string{className}, resolver.ComputeMRO(parents[0], heritage)...)
	}

	// C3 linearization for multiple inheritance
	parentMROs := make([][]string, len(parents))
	for i, parent := range parents {
		parentMROs[i] = resolver.ComputeMRO(parent, heritage)
	}

	merged := c3Merge(append(parentMROs, parents))
	return append([]string{className}, merged...)
}

func buildParentMap(heritage []model.RawHeritage) map[string][]string {
	parentMap := make(map[string][]string)
	for _, entry := range heritage {
		parentMap[entry.ChildName] = append(parentMap[entry.ChildName], entry.ParentName)
	}
	return parentMap
}

// c3Merge implements the merge step of C3 linearization.
// Input: a list of sequences — each parent's MRO plus the direct parent list.
// Output: a single merged list preserving local precedence and monotonicity.
//
// At each step, the algorithm picks the first head (seq[0]) that does not appear
// in the tail (seq[1:]) of any other sequence. If no such head exists, the
// hierarchy is inconsistent and remaining elements are appended as-is.
func c3Merge(sequences [][]string) []string {
	var result []string

	seqs := make([][]string, len(sequences))
	for i, seq := range sequences {
		seqs[i] = make([]string, len(seq))
		copy(seqs[i], seq)
	}

	for {
		var nonEmpty [][]string
		for _, seq := range seqs {
			if len(seq) > 0 {
				nonEmpty = append(nonEmpty, seq)
			}
		}
		seqs = nonEmpty
		if len(seqs) == 0 {
			break
		}

		found := false
		for _, seq := range seqs {
			head := seq[0]

			inTail := false
			for _, other := range seqs {
				for _, element := range other[1:] {
					if element == head {
						inTail = true
						break
					}
				}
				if inTail {
					break
				}
			}

			if !inTail {
				result = append(result, head)
				for i := range seqs {
					if len(seqs[i]) > 0 && seqs[i][0] == head {
						seqs[i] = seqs[i][1:]
					}
				}
				found = true
				break
			}
		}

		if !found {
			for _, seq := range seqs {
				result = append(result, seq...)
			}
			break
		}
	}
	return result
}
