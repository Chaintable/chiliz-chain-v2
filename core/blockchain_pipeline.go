//go:build pipeline

package core

import (
	"fmt"

	"github.com/Chaintable/pipeline/tracer"
	ptypes "github.com/Chaintable/pipeline/types"
	"github.com/Chaintable/pipeline/util"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"golang.org/x/exp/slices"
)

func init() {
	pipelineNotifyCanonicalBlock = pipelineNotifyCanonicalBlockImpl
}

func pipelineNotifyCanonicalBlockImpl(bc *BlockChain, block *types.Block) {
	// 先确保 pipeline tracer 不为空，然后再判断是否需要 push kafka
	// 上一个 push kafka 的 block 必然存在（至少 genesis block）
	// 上一个 push kafka 的 block 比当前 head 更新，说明有回退/unwind，不处理
	if tracer.NodeXPusher == nil || tracer.NodeXPusher.IsBackup {
		return
	}
	last := tracer.NodeXPusher.LastPushedBlock()
	if last == nil || last.BlockNumber > block.NumberU64() {
		return
	}

	_, dropBlocks, newBlocks := bc.getCommonAncestor(*last, ptypes.BlockContext{
		BlockNumber: block.NumberU64(),
		Hash:        block.Hash(),
		ParentHash:  block.ParentHash(),
		Timestamp:   block.Time(),
	})

	var blockChange *ptypes.BlockChangeNotification
	if len(dropBlocks) > 0 {
		blockChange = &ptypes.BlockChangeNotification{ChangeType: 2, NewBlocks: newBlocks, DropBlocks: dropBlocks}
	} else if len(newBlocks) > 0 {
		blockChange = &ptypes.BlockChangeNotification{ChangeType: 1, NewBlocks: newBlocks}
	}
	if blockChange == nil {
		return
	}
	if err := tracer.NodeXPusher.PushBlockChangeNotification(blockChange); err != nil {
		log.Error("PushBlockChangeNotification error", "err", err)
		return
	}
	log.Info("NodeXPusher PushBlockChangeNotification", "blockChange", blockChange)
}

// 返回两个块的共同祖先，以及两个块的从共同祖先到两个块的路径（drop/new）。
func (bc *BlockChain) getCommonAncestor(blocka ptypes.BlockContext, blockb ptypes.BlockContext) (ptypes.BlockContext, []ptypes.BlockContext, []ptypes.BlockContext) {
	var (
		chainA, chainB []ptypes.BlockContext
	)
	if blockb.ParentHash == blocka.Hash {
		return blocka, chainA, []ptypes.BlockContext{blockb}
	}
	for blockb.BlockNumber > blocka.BlockNumber {
		chainB = append(chainB, blockb)
		headerb := bc.GetHeaderByHash2(blockb.ParentHash)
		if headerb == nil {
			log.Crit("Failed to get header by hash", "hash", blockb.ParentHash)
		}
		blockb = ptypes.BlockContext{BlockNumber: headerb.Number.Uint64(), Hash: headerb.Hash(), ParentHash: headerb.ParentHash, Timestamp: headerb.Time}
	}
	for blocka.Hash != blockb.Hash {
		chainA = append(chainA, blocka)
		headera := bc.GetHeaderByHash2(blocka.ParentHash)
		if headera == nil {
			log.Crit("Failed to get header by hash", "hash", blocka.ParentHash)
		}
		blocka = ptypes.BlockContext{BlockNumber: headera.Number.Uint64(), Hash: headera.Hash(), ParentHash: headera.ParentHash, Timestamp: headera.Time}

		chainB = append(chainB, blockb)
		headerb := bc.GetHeaderByHash2(blockb.ParentHash)
		if headerb == nil {
			log.Crit("Failed to get header by hash", "hash", blockb.ParentHash)
		}
		blockb = ptypes.BlockContext{BlockNumber: headerb.Number.Uint64(), Hash: headerb.Hash(), ParentHash: headerb.ParentHash, Timestamp: headerb.Time}
	}
	// now blocka == blockb == ancestor

	slices.Reverse(chainA)
	slices.Reverse(chainB)
	return blocka, chainA, chainB
}

func (bc *BlockChain) GetHeaderByHash2(blockHash common.Hash) *types.Header {
	header := bc.GetHeaderByHash(blockHash)
	if header != nil {
		return header
	}
	if tracer.NodeXPusher == nil {
		return nil
	}
	fallback := &types.Header{}
	err := util.DownloadFileFromS3Json(tracer.NodeXPusher.Uploader, tracer.NodeXPusher.Bucket, fmt.Sprintf("%s/%s/block", tracer.BizChainID, blockHash.String()), fallback)
	if err != nil {
		log.Error("GetHeaderByHash2 DownloadFileFromS3Json error", "err", err)
		return nil
	}
	return fallback
}
