// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"math"

	"github.com/bitgo/rmgd/chaincfg/chainhash"
	"github.com/bitgo/rmgd/rmgutil"
)

// nextPowerOfTwo returns the next highest power of two from a given number if
// it is not already a power of two.  This is a helper function used during the
// calculation of a merkle tree.
func nextPowerOfTwo(n int) int {
	// Return the number if it's already a power of 2.
	if n&(n-1) == 0 {
		return n
	}

	// Figure out and return the next power of two.
	exponent := uint(math.Log2(float64(n))) + 1
	return 1 << exponent // 2^exponent
}

// HashMerkleBranches takes two hashes, treated as the left and right tree
// nodes, and returns the hash of their concatenation.  This is a helper
// function used to aid in the generation of a merkle tree.
func HashMerkleBranches(left *chainhash.Hash, right *chainhash.Hash) *chainhash.Hash {
	// Concatenate the left and right nodes.
	var hash [chainhash.HashSize * 2]byte
	copy(hash[:chainhash.HashSize], left[:])
	copy(hash[chainhash.HashSize:], right[:])

	newHash := chainhash.DoubleHashH(hash[:])
	return &newHash
}

// BuildMerkleTreeStore creates a merkle tree from a slice of transactions,
// stores it using a linear array, and returns a slice of the backing array.  A
// linear array was chosen as opposed to an actual tree structure since it uses
// about half as much memory.  The following describes a merkle tree and how it
// is stored in a linear array.
//
// A merkle tree is a tree in which every non-leaf node is the hash of its
// children nodes.  A diagram depicting how this works for bitcoin transactions
// where h(x) is a double sha256 follows:
//
//	         root = h1234 = h(h12 + h34)
//	        /                           \
//	  h12 = h(h1 + h2)            h34 = h(h3 + h4)
//	   /            \              /            \
//	h1 = h(stx1) h2 = h(stx2)   h3 = h(tx1)  h4 = h(tx2)
//
// The above stored as a linear array is as follows:
//
// 	[h1 h2 h3 h4 h12 h34 root]
//
// As the above shows, the merkle root is always the last element in the array.
//
// The number of inputs is not always a power of two which results in a
// balanced tree structure as above.  In that case, parent nodes with no
// children are also zero and parent nodes with only a single left node
// are calculated by concatenating the left node with itself before hashing.
// Since this function uses nodes that are pointers to the hashes, empty nodes
// will be nil.
func BuildMerkleTreeStore(transactions []*rmgutil.Tx) []*chainhash.Hash {
	// Calculate how many entries are required to hold the binary merkle
	// tree as a linear array and create an array of that size.
	nextPoT := nextPowerOfTwo(len(transactions))
	// A merkle tree with some N == Power-of-Two-number of leaves has N - 1 nodes.
	// This would require an array length of nextPoT * 2 - 1.
	// We want both types of txHash represented, stripped and unstripped,
	// hence, the size is calculated as nextPot * 4 - 1
	arraySize := nextPoT*2*2 - 1
	merkles := make([]*chainhash.Hash, arraySize)

	// Create the base transaction hashes and populate the array with them.
	for i, tx := range transactions {
		merkles[i] = tx.Hash()
		merkles[i+nextPoT] = tx.HashWithSig()
	}

	// Start the array offset after the last transaction and adjusted to the
	// next power of two.
	offset := nextPoT * 2
	for i := 0; i < arraySize-1; i += 2 {
		switch {
		// When there is no left child node, the parent is nil too.
		case merkles[i] == nil:
			merkles[offset] = nil

		// When there is no right child, the parent is generated by
		// hashing the concatenation of the left child with itself.
		case merkles[i+1] == nil:
			newHash := HashMerkleBranches(merkles[i], merkles[i])
			merkles[offset] = newHash

		// The normal case sets the parent node to the double sha256
		// of the concatentation of the left and right children.
		default:
			newHash := HashMerkleBranches(merkles[i], merkles[i+1])
			merkles[offset] = newHash
		}
		offset++
	}

	return merkles
}
