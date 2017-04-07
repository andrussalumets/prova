// Copyright (c) 2013-2016 The btcsuite developers
// Copyright (c) 2017 BitGo
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"fmt"

	"github.com/bitgo/prova/chaincfg"
	"github.com/bitgo/prova/chaincfg/chainhash"
	"github.com/bitgo/prova/database"
	"github.com/bitgo/prova/provautil"
	"github.com/bitgo/prova/txscript"
)

// CheckpointConfirmations is the number of blocks before the end of the current
// best block chain that a good checkpoint candidate must be.
const CheckpointConfirmations = 2016

// newHashFromStr converts the passed big-endian hex string into a
// chainhash.Hash.  It only differs from the one available in chainhash in that
// it ignores the error since it will only (and must only) be called with
// hard-coded, and therefore known good, hashes.
func newHashFromStr(hexStr string) *chainhash.Hash {
	hash, _ := chainhash.NewHashFromStr(hexStr)
	return hash
}

// Checkpoints returns a slice of checkpoints (regardless of whether they are
// already known).
// When there are no checkpoints for the chain, it will return nil.
//
// This function is safe for concurrent access.
func (b *BlockChain) Checkpoints() []chaincfg.Checkpoint {
	return b.checkpoints
}

// HasCheckpoints returns whether this BlockChain has checkpoints defined.
//
// This function MUST be called with the chain state lock held (for reads).
// This function is safe for concurrent access.
func (b *BlockChain) HasCheckpoints() bool {
	return len(b.checkpoints) > 0
}

// LatestCheckpoint returns the most recent checkpoint (regardless of whether they
// are already known). When there are no defined checkpoints for the active chain
// instance, it will return nil.
//
// This function is safe for concurrent access.
func (b *BlockChain) LatestCheckpoint() *chaincfg.Checkpoint {
	if !b.HasCheckpoints() {
		return nil
	}
	return &b.checkpoints[len(b.checkpoints)-1]
}

// verifyCheckpoint returns whether the passed block height and hash combination
// match the checkpoint data. It also returns true if there is no checkpoint
// data for the passed block height.
func (b *BlockChain) verifyCheckpoint(height uint32, hash *chainhash.Hash) bool {
	if !b.HasCheckpoints() {
		return true
	}

	// Nothing to check if there is no checkpoint data for the block height.
	checkpoint, exists := b.checkpointsByHeight[height]
	if !exists {
		return true
	}

	if !checkpoint.Hash.IsEqual(hash) {
		return false
	}

	log.Infof("Verified checkpoint at height %d/block %s", checkpoint.Height,
		checkpoint.Hash)
	return true
}

// findPreviousCheckpoint finds the most recent checkpoint that is already
// available in the downloaded portion of the block chain and returns the
// associated block.  It returns nil if a checkpoint can't be found (this should
// really only happen for blocks before the first checkpoint).
//
// This function MUST be called with the chain lock held (for reads).
func (b *BlockChain) findPreviousCheckpoint() (*provautil.Block, error) {
	if !b.HasCheckpoints() {
		return nil, nil
	}

	checkpoints := b.checkpoints
	numCheckpoints := len(checkpoints)
	if numCheckpoints == 0 {
		// No checkpoints
		return nil, nil
	}

	// Perform the initial search to find and cache the latest known
	// checkpoint if the best chain is not known yet or we haven't already
	// previously searched.
	if b.checkpointBlock == nil && b.nextCheckpoint == nil {
		// Loop backwards through the available checkpoints to find one
		// that is already available.
		checkpointIndex := -1
		err := b.db.View(func(dbTx database.Tx) error {
			for i := numCheckpoints - 1; i >= 0; i-- {
				if dbMainChainHasBlock(dbTx, checkpoints[i].Hash) {
					checkpointIndex = i
					break
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}

		// No known latest checkpoint.  This will only happen on blocks
		// before the first known checkpoint.  So, set the next expected
		// checkpoint to the first checkpoint and return the fact there
		// is no latest known checkpoint block.
		if checkpointIndex == -1 {
			b.nextCheckpoint = &checkpoints[0]
			return nil, nil
		}

		// Cache the latest known checkpoint block for future lookups.
		checkpoint := checkpoints[checkpointIndex]
		err = b.db.View(func(dbTx database.Tx) error {
			block, err := dbFetchBlockByHash(dbTx, checkpoint.Hash)
			if err != nil {
				return err
			}
			b.checkpointBlock = block

			// Set the next expected checkpoint block accordingly.
			b.nextCheckpoint = nil
			if checkpointIndex < numCheckpoints-1 {
				b.nextCheckpoint = &checkpoints[checkpointIndex+1]
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		return b.checkpointBlock, nil
	}

	// At this point we've already searched for the latest known checkpoint,
	// so when there is no next checkpoint, the current checkpoint lockin
	// will always be the latest known checkpoint.
	if b.nextCheckpoint == nil {
		return b.checkpointBlock, nil
	}

	// When there is a next checkpoint and the height of the current best
	// chain does not exceed it, the current checkpoint lockin is still
	// the latest known checkpoint.
	if b.bestNode.height < b.nextCheckpoint.Height {
		return b.checkpointBlock, nil
	}

	// We've reached or exceeded the next checkpoint height.  Note that
	// once a checkpoint lockin has been reached, forks are prevented from
	// any blocks before the checkpoint, so we don't have to worry about the
	// checkpoint going away out from under us due to a chain reorganize.

	// Cache the latest known checkpoint block for future lookups.  Note
	// that if this lookup fails something is very wrong since the chain
	// has already passed the checkpoint which was verified as accurate
	// before inserting it.
	err := b.db.View(func(tx database.Tx) error {
		block, err := dbFetchBlockByHash(tx, b.nextCheckpoint.Hash)
		if err != nil {
			return err
		}
		b.checkpointBlock = block
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Set the next expected checkpoint.
	checkpointIndex := -1
	for i := numCheckpoints - 1; i >= 0; i-- {
		if checkpoints[i].Hash.IsEqual(b.nextCheckpoint.Hash) {
			checkpointIndex = i
			break
		}
	}
	b.nextCheckpoint = nil
	if checkpointIndex != -1 && checkpointIndex < numCheckpoints-1 {
		b.nextCheckpoint = &checkpoints[checkpointIndex+1]
	}

	return b.checkpointBlock, nil
}

// isNonstandardTransaction determines whether a transaction contains any
// scripts which are not one of the standard types.
func isNonstandardTransaction(tx *provautil.Tx) bool {
	// Check all of the output public key scripts for non-standard scripts.
	for _, txOut := range tx.MsgTx().TxOut {
		scriptClass := txscript.GetScriptClass(txOut.PkScript)
		if scriptClass == txscript.NonStandardTy {
			return true
		}
	}
	return false
}

// IsCheckpointCandidate returns whether or not the passed block is a good
// checkpoint candidate.
//
// The factors used to determine a good checkpoint are:
//  - The block must be in the main chain
//  - The block must be at least 'CheckpointConfirmations' blocks prior to the
//    current end of the main chain
//  - The timestamps for the blocks before and after the checkpoint must have
//    timestamps which are also before and after the checkpoint, respectively
//    (due to the median time allowance this is not always the case)
//  - The block must not contain any strange transaction such as those with
//    nonstandard scripts
//
// The intent is that candidates are reviewed by a developer to make the final
// decision and then manually added to the list of checkpoints for a network.
//
// This function is safe for concurrent access.
func (b *BlockChain) IsCheckpointCandidate(block *provautil.Block) (bool, error) {
	b.chainLock.RLock()
	defer b.chainLock.RUnlock()

	var isCandidate bool
	err := b.db.View(func(dbTx database.Tx) error {
		// A checkpoint must be in the main chain.
		blockHeight, err := dbFetchHeightByHash(dbTx, block.Hash())
		if err != nil {
			// Only return an error if it's not due to the block not
			// being in the main chain.
			if !isNotInMainChainErr(err) {
				return err
			}
			return nil
		}

		// Ensure the height of the passed block and the entry for the
		// block in the main chain match.  This should always be the
		// case unless the caller provided an invalid block.
		if blockHeight != block.Height() {
			return fmt.Errorf("passed block height of %d does not "+
				"match the main chain height of %d",
				block.Height(), blockHeight)
		}

		// A checkpoint must be at least CheckpointConfirmations blocks
		// before the end of the main chain.
		mainChainHeight := b.bestNode.height
		if blockHeight > (mainChainHeight - CheckpointConfirmations) {
			return nil
		}

		// Get the previous block header.
		prevHash := &block.MsgBlock().Header.PrevBlock
		prevHeader, err := dbFetchHeaderByHash(dbTx, prevHash)
		if err != nil {
			return err
		}

		// Get the next block header.
		nextHeader, err := dbFetchHeaderByHeight(dbTx, blockHeight+1)
		if err != nil {
			return err
		}

		// A checkpoint must have timestamps for the block and the
		// blocks on either side of it in order (due to the median time
		// allowance this is not always the case).
		prevTime := prevHeader.Timestamp
		curTime := block.MsgBlock().Header.Timestamp
		nextTime := nextHeader.Timestamp
		if prevTime.After(curTime) || nextTime.Before(curTime) {
			return nil
		}

		// A checkpoint must have transactions that only contain
		// standard scripts.
		for _, tx := range block.Transactions() {
			if isNonstandardTransaction(tx) {
				return nil
			}
		}

		// All of the checks passed, so the block is a candidate.
		isCandidate = true
		return nil
	})
	return isCandidate, err
}
