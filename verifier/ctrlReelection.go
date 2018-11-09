// Copyright 2018 The MATRIX Authors as well as Copyright 2014-2017 The go-ethereum Authors
// This file is consisted of the MATRIX library and part of the go-ethereum library.
//
// The MATRIX-ethereum library is free software: you can redistribute it and/or modify it under the terms of the MIT License.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"),
// to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, 
//and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject tothe following conditions:
//
//The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.
//
//THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
//FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, 
//WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISINGFROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE
//OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
package verifier

import (
	"github.com/matrix/go-matrix/ca"
	"github.com/matrix/go-matrix/common"
	"github.com/matrix/go-matrix/core/types"
	"github.com/matrix/go-matrix/log"
	"github.com/matrix/go-matrix/mc"
	"github.com/matrix/go-matrix/params/man"
	"github.com/pkg/errors"
	"time"
)

func (self *controller) startReelect(reelectTurn uint32) {
	log.INFO(self.logInfo, "重选流程", "开启处理", "重选轮次", reelectTurn, "共识轮次", self.dc.curConsensusTurn, "高度", self.dc.number)
	if self.State() != stReelect {
		log.INFO(self.logInfo, "开启重选流程", "当前状态不是重选状态，不处理", "状态", self.State(), "重选轮次", reelectTurn, "共识轮次", self.dc.curConsensusTurn, "高度", self.dc.number)
		return
	}
	if self.dc.curReelectTurn == reelectTurn {
		log.INFO(self.logInfo, "开启重选流程", "重选已启动，不处理", "重选轮次", reelectTurn, "共识轮次", self.dc.curConsensusTurn, "高度", self.dc.number)
		return
	}

	if err := self.dc.SetReelectTurn(reelectTurn); err != nil {
		log.ERROR(self.logInfo, "开启重选流程", "设置重选轮次失败", "err", err, "高度", self.dc.number)
		return
	}
	master := self.dc.GetReelectMaster()
	if master == ca.GetAddress() {
		log.INFO(self.logInfo, ">>>>开启重选流程(master)", master.Hex(), "轮次", self.curTurnInfo(), "高度", self.dc.number)
		self.setTimer(man.LRSReelectInterval, self.reelectTimer)
		self.sendInquiryReq()
	} else {
		log.INFO(self.logInfo, ">>>>开启重选流程(follower)", master.Hex(), "轮次", self.curTurnInfo(), "高度", self.dc.number)
		self.setTimer(0, self.reelectTimer)
	}

	self.sendLeaderMsg()
}

func (self *controller) finishReelectWithPOS(posResult *mc.HD_BlkConsensusReqMsg, from common.Address) {
	log.INFO(self.logInfo, "完成重阶段(POS结果)", "开始处理")
	defer log.INFO(self.logInfo, "完成重阶段(POS结果)", "结束处理")
	mc.PublishEvent(mc.Leader_RecoveryState, &mc.RecoveryStateMsg{Type: mc.RecoveryTypePOS, Header: posResult.Header, From: from})
	self.setTimer(0, self.timer)
	self.setTimer(0, self.reelectTimer)
	self.dc.state = stMining
	self.dc.SetReelectTurn(0)
	self.selfCache.ClearSelfInquiryMsg()
	self.sendLeaderMsg()
}

func (self *controller) finishReelectWithRLConsensus(rlResult *mc.HD_ReelectLeaderConsensus) {
	log.INFO(self.logInfo, "完成重阶段(leader重选结果)", "开始处理")
	defer log.INFO(self.logInfo, "完成重阶段(leader重选结果)", "结束处理")
	consensusTurn := rlResult.Req.InquiryReq.ConsensusTurn + rlResult.Req.InquiryReq.ReelectTurn
	log.INFO(self.logInfo, "完成重选阶段(leader重选结果), 共识轮次", consensusTurn)
	if err := self.dc.SetConsensusTurn(consensusTurn); err != nil {
		log.ERROR(self.logInfo, "完成重选阶段(leader重选结果)", "设置共识轮次失败", "err", err, "目标共识轮次", consensusTurn)
		return
	}

	self.setTimer(0, self.reelectTimer)
	self.selfCache.ClearSelfInquiryMsg()
	//以重选请求的时间戳为本轮次的开始时间戳
	self.dc.turnTime.SetBeginTime(consensusTurn, rlResult.Req.TimeStamp)
	curTime := time.Now().Unix()
	st, remainTime, reelectTurn := self.dc.turnTime.CalState(consensusTurn, curTime)
	log.INFO(self.logInfo, "完成重选阶段(leader重选结果)", "计算当前状态结果", "状态", st, "剩余时间", remainTime, "重选轮次", reelectTurn)
	self.dc.state = st
	self.dc.curReelectTurn = 0
	self.setTimer(remainTime, self.timer)
	if st == stPos {
		self.processPOSState()
	} else if st == stReelect {
		self.startReelect(reelectTurn)
	}

	self.sendLeaderMsg()
}

func (self *controller) reelectTimeOutHandle() {
	if self.State() != stReelect {
		log.ERROR(self.logInfo, "重选定时器超时处理", "状态错误,当前状态不是重选阶段", "当前状态", self.State().String())
		return
	}
	switch self.selfCache.InquiryResult() {
	case mc.ReelectRSPTypeAgree:
		self.sendRLReq()
	case mc.ReelectRSPTypePOS, mc.ReelectRSPTypeAlreadyRL:
		self.sendResultBroadcastMsg()
	default:
		self.sendInquiryReq()
	}
	self.setTimer(man.LRSReelectInterval, self.reelectTimer)
}

func (self *controller) handleInquiryReq(req *mc.HD_ReelectInquiryReqMsg) {
	if self.State() == stIdle {
		log.WARN(self.logInfo, "处理重选询问请求", "当前状态为idle，忽略消息", "from", req.From.Hex(), "高度", self.dc.number)
		return
	}
	if nil == req {
		log.WARN(self.logInfo, "处理重选询问请求", "消息为nil")
		return
	}
	if req.Master != req.From {
		log.WARN(self.logInfo, "处理重选询问请求", "消息master与from不匹配", "master", req.Master.Hex(), "from", req.From.Hex())
		return
	}

	master, err := self.dc.GetLeader(req.ConsensusTurn + req.ReelectTurn)
	if err != nil {
		log.ERROR(self.logInfo, "处理重选询问请求", "验证消息合法性错误", "计算master失败", err)
		return
	}

	if master != req.From {
		log.ERROR(self.logInfo, "处理重选询问请求", "消息不合法，master不匹配", "from", req.From.Hex(), "local master", master.Hex())
		return
	}

	if req.ConsensusTurn > self.dc.curConsensusTurn {
		// 消息轮次>本地轮次: 共识轮次落后，缓存消息(todo 可以考虑请求from的共识状态)
		log.INFO(self.logInfo, "处理重选询问请求", "共识轮次低于对方，缓存消息", "消息共识轮次", req.ConsensusTurn, "本地共识轮次", self.dc.curConsensusTurn, "高度", self.dc.number)
		self.mp.SaveInquiryReqMsg(req)
		return
	} else if req.ConsensusTurn == self.dc.curConsensusTurn {
		switch self.State() {
		case stMining:
			self.sendInquiryRspWithPOS(types.RlpHash(req), req.From, req.Number)

		case stReelect:
			if req.ReelectTurn != self.dc.curReelectTurn {
				log.INFO(self.logInfo, "处理重选询问请求", "重选轮次不匹配，忽略消息", "消息重选轮次", req.ReelectTurn, "本地重选轮次", self.dc.curReelectTurn, "高度", self.dc.number)
				return
			}
			self.sendInquiryRspWithAgree(types.RlpHash(req), req.From, req.Number)

		default:
			log.INFO(self.logInfo, "处理重选询问请求", "本地状态不匹配，不响应请求", "本地状态", self.State())
			return
		}
	} else {
		// 消息轮次<本地轮次: 请求方共识轮次落后
		self.sendInquiryRspWithRLConsensus(types.RlpHash(req), req.From, req.Number)
	}
}

func (self *controller) handleInquiryRsp(rsp *mc.HD_ReelectInquiryRspMsg) {
	log.INFO(self.logInfo, "处理重选询问响应", "开始", "from", rsp.From.Hex(), "hash", rsp.ReqHash.TerminalString(), "类型", rsp.Type)
	if self.selfCache.InquiryResult() != mc.ReelectRSPTypeNone {
		log.INFO(self.logInfo, "处理重选询问响应", "当前已有询问结果，忽略消息", "当前询问结果", self.selfCache.InquiryResult())
		return
	}

	if err := self.selfCache.CheckInquiryRspMsg(rsp); err != nil {
		log.WARN(self.logInfo, "处理重选询问响应", "检查消息错误", "高度", self.dc.number, "err", err)
		return
	}
	switch rsp.Type {
	case mc.ReelectRSPTypeNewBlockReady:
		self.processNewBlockReadyRsp(rsp.NewBlock, rsp.From)
		return

	case mc.ReelectRSPTypePOS, mc.ReelectRSPTypeAlreadyRL:
		if err := self.selfCache.SetInquiryResultNotAgree(rsp.Type, rsp); err != nil {
			log.ERROR(self.logInfo, "处理重选询问响应", "设置询问结果错误", "结果类型", rsp.Type, "err", err)
			return
		}
		self.sendResultBroadcastMsg()

	case mc.ReelectRSPTypeAgree:
		if err := self.selfCache.SaveInquiryAgreeSign(rsp); err != nil {
			log.ERROR(self.logInfo, "处理重选询问响应(同意更换leader响应)", "保存同意签名错误", "err", err)
			return
		}

		signs := self.selfCache.GetInquiryAgreeSigns()
		log.INFO(self.logInfo, "处理重选询问响应(同意更换leader响应)", "保存签名成功", "签名总数", len(signs))
		rightSigns, err := self.matrix.DPOSEngine().VerifyHashWithVerifiedSignsAndNumber(self.dc, signs, self.dc.number)
		if err != nil {
			log.INFO(self.logInfo, "处理重选询问响应(同意更换leader响应)", "同意的签名没有通过POS共识", "err", err)
			return
		}

		log.INFO(self.logInfo, "处理重选询问响应(同意更换leader响应)", "POS共识通过, 准备发送更换leader请求")
		if err := self.selfCache.SetInquiryResultAgree(rightSigns); err != nil {
			log.ERROR(self.logInfo, "处理重选询问响应(同意更换leader响应)", "设置询问结果错误", "err", err)
			return
		}
		self.sendRLReq()
	}
}

func (self *controller) handleRLReq(req *mc.HD_ReelectLeaderReqMsg) {
	log.INFO(self.logInfo, "leader重选请求处理", "开始", "高度", self.dc.number, "共识轮次", req.InquiryReq.ConsensusTurn, "重选轮次", req.InquiryReq.ReelectTurn, "from", req.InquiryReq.From.Hex())
	if self.State() != stReelect {
		log.ERROR(self.logInfo, "leader重选请求处理", "本地状态错误", "本地状态", self.State())
		return
	}
	if err := self.checkRLReqMsg(req); err != nil {
		log.ERROR(self.logInfo, "leader重选请求处理", "消息异常", "err", err)
		return
	}

	hash := types.RlpHash(req)
	sign, err := self.matrix.SignHelper().SignHashWithValidate(hash.Bytes(), true)
	if err != nil {
		log.ERROR(self.logInfo, "leader重选请求处理", "签名错误", "err", err)
		return
	}
	rsp := &mc.HD_ReelectLeaderVoteMsg{
		Number: self.dc.number,
		Vote: mc.HD_ConsensusVote{
			SignHash: hash,
			Round:    uint64(self.dc.curReelectTurn + self.dc.curConsensusTurn),
			Sign:     sign,
		},
	}
	self.matrix.HD().SendNodeMsg(mc.HD_LeaderReelectVote, rsp, common.RoleNil, []common.Address{req.InquiryReq.From})
}

func (self *controller) handleRLVote(vote *mc.HD_ReelectLeaderVoteMsg) {
	if err := self.selfCache.SaveRLVote(vote); err != nil {
		log.ERROR(self.logInfo, "处理leader重选响应", "保存签名错误", "err", err)
		return
	}
	rightSigns, err := self.matrix.DPOSEngine().VerifyHashWithVerifiedSignsAndNumber(self.dc, self.selfCache.GetRLSigns(), self.dc.number)
	if err != nil {
		log.INFO(self.logInfo, "处理leader重选响应", "签名没有通过POS共识", "err", err)
		return
	}

	log.INFO(self.logInfo, "处理leader重选响应", "POS共识通过, 准备发送重选结果广播")

	if err := self.selfCache.SetRLResultBroadcastSuccess(rightSigns); err != nil {
		log.ERROR(self.logInfo, "处理leader重选响应", "设置重选结果广播(leader重选成功)消息错误", "err", err)
		return
	}
	self.sendResultBroadcastMsg()
}

func (self *controller) handleResultBroadcastMsg(msg *mc.HD_ReelectResultBroadcastMsg) {
	if nil == msg {
		log.WARN(self.logInfo, "处理重选结果广播", "消息为nil")
		return
	}
	if err := self.processResultBroadcastMsg(msg); err != nil {
		log.ERROR(self.logInfo, "处理重选结果广播失败", err)
		return
	}
	self.sendResultBroadcastRsp(msg)
}

func (self *controller) handleResultRsp(rsp *mc.HD_ReelectResultRspMsg) {
	if err := self.selfCache.SaveResultRsp(rsp); err != nil {
		log.ERROR(self.logInfo, "处理重选结果广播响应", "保存响应失败", "err", err)
		return
	}
	_, err := self.matrix.DPOSEngine().VerifyHashWithVerifiedSignsAndNumber(self.dc, self.selfCache.GetResultRspSigns(), self.dc.number)
	if err != nil {
		log.INFO(self.logInfo, "处理重选结果广播响应", "响应没有通过POS共识", "err", err)
		return
	}
	log.INFO(self.logInfo, "处理重选结果广播响应", "POS共识通过, 准备处理广播结果，切换状态")
	resultMsg, err := self.selfCache.GetLocalResultMsg()
	if err != nil {
		log.ERROR(self.logInfo, "处理本地重选结果广播", "获取本地重选结果广播错误", "err", err)
		return
	}
	if err := self.processResultBroadcastMsg(resultMsg); err != nil {
		log.ERROR(self.logInfo, "处理本地重选结果广播失败", err)
		return
	}
}

func (self *controller) processResultBroadcastMsg(msg *mc.HD_ReelectResultBroadcastMsg) error {
	if msg == nil {
		return ErrMsgIsNil
	}
	switch msg.Type {
	case mc.ReelectRSPTypePOS:
		posResult := msg.POSResult
		if nil == posResult || nil == posResult.Header {
			return ErrPOSResultIsNil
		}
		if posResult.Header.Leader != self.dc.GetConsensusLeader() {
			return errors.Errorf("消息中headerLeader(%s) != 本地共识leader(%s)", posResult.Header.Leader.Hex(), self.dc.GetConsensusLeader().Hex())
		}
		if err := self.matrix.DPOSEngine().VerifyBlock(self.dc, posResult.Header); err != nil {
			return errors.Errorf("POS完成结果中的POS结果验证错误(%v)", err)
		}
		self.finishReelectWithPOS(posResult, msg.From)

	case mc.ReelectRSPTypeAgree, mc.ReelectRSPTypeAlreadyRL:
		rlResult := msg.RLResult
		if nil == rlResult {
			return ErrLeaderResultIsNil
		}
		turn := rlResult.Req.InquiryReq.ConsensusTurn + rlResult.Req.InquiryReq.ReelectTurn
		if turn < self.dc.curConsensusTurn {
			return errors.Errorf("消息轮次(%d) <= 本地共识轮次(%d)", turn, self.dc.curConsensusTurn)
		}
		if rlResult.TimeStamp < rlResult.Req.TimeStamp {
			return errors.Errorf("时间戳检查异常, 共识完成时间戳(%d)比共识请求时间戳(%d)早", rlResult.TimeStamp, rlResult.Req.TimeStamp)
		}
		if _, err := self.matrix.DPOSEngine().VerifyHashWithNumber(self.dc, types.RlpHash(rlResult.Req), rlResult.Votes, self.dc.number); err != nil {
			return errors.Errorf("leader重选完成中的POS结果验证错误(%v)", err)
		}
		self.finishReelectWithRLConsensus(rlResult)
	default:
		return errors.Errorf("结果类型(%v)错误", msg.Type)
	}
	return nil
}

func (self *controller) sendInquiryReq() {
	req := &mc.HD_ReelectInquiryReqMsg{
		Number:        self.dc.number,
		ConsensusTurn: self.dc.curConsensusTurn,
		ReelectTurn:   self.dc.curReelectTurn,
		TimeStamp:     time.Now().Unix(),
		Master:        ca.GetAddress(),
		From:          ca.GetAddress(),
	}
	self.selfCache.SetInquiryReq(req)
	log.INFO(self.logInfo, "send<重选询问请求>, hash", self.selfCache.inquiryHash.TerminalString(), "轮次", self.curTurnInfo(), "高度", self.Number())
	self.matrix.HD().SendNodeMsg(mc.HD_LeaderReelectInquiryReq, req, common.RoleValidator, nil)

	return
}

func (self *controller) sendInquiryRspWithPOS(repHash common.Hash, target common.Address, number uint64) {
	POSMsg, err := self.mp.GetPOSNotifyMsg(self.dc.GetConsensusLeader())
	if err != nil {
		log.ERROR(self.logInfo, "send<询问响应(POS完成响应)>", "获取POS通知消息错误", "err", err, "高度", self.Number(),
			"共识轮次", self.dc.curConsensusTurn, "共识leader", self.dc.GetConsensusLeader())
		return
	}
	rsp := &mc.HD_ReelectInquiryRspMsg{
		Number:    number,
		ReqHash:   repHash,
		Type:      mc.ReelectRSPTypePOS,
		AgreeSign: common.Signature{},
		POSResult: &mc.HD_BlkConsensusReqMsg{Header: POSMsg.Header, TxsCode: POSMsg.TxsCode},
		RLResult:  nil,
		NewBlock:  nil,
	}
	self.matrix.HD().SendNodeMsg(mc.HD_LeaderReelectInquiryRsp, rsp, common.RoleNil, []common.Address{target})
}

func (self *controller) sendInquiryRspWithAgree(repHash common.Hash, target common.Address, number uint64) {
	sign, err := self.matrix.SignHelper().SignHashWithValidate(repHash.Bytes(), true)
	if err != nil {
		log.ERROR(self.logInfo, "send<询问响应(同意更换leader响应)>", "签名失败", "err", err, "高度", self.Number(),
			"共识轮次", self.dc.curConsensusTurn, "重选轮次", self.dc.curReelectTurn)
		return
	}
	rsp := &mc.HD_ReelectInquiryRspMsg{
		Number:    number,
		ReqHash:   repHash,
		Type:      mc.ReelectRSPTypeAgree,
		AgreeSign: sign,
		POSResult: nil,
		RLResult:  nil,
		NewBlock:  nil,
	}
	self.matrix.HD().SendNodeMsg(mc.HD_LeaderReelectInquiryRsp, rsp, common.RoleNil, []common.Address{target})
}

func (self *controller) sendInquiryRspWithRLConsensus(repHash common.Hash, target common.Address, number uint64) {
	consensusMsg, err := self.mp.GetRLConsensusMsg(self.dc.GetConsensusLeader())
	if err != nil {
		log.ERROR(self.logInfo, "send<询问响应(leader重选已完成)>", "获取leader重选共识消息错误", "err", err, "高度", self.Number(),
			"共识轮次", self.dc.curConsensusTurn, "共识leader", self.dc.GetConsensusLeader())
		return
	}
	rsp := &mc.HD_ReelectInquiryRspMsg{
		Number:    number,
		ReqHash:   repHash,
		Type:      mc.ReelectRSPTypeAlreadyRL,
		AgreeSign: common.Signature{},
		POSResult: nil,
		RLResult:  consensusMsg,
		NewBlock:  nil,
	}
	self.matrix.HD().SendNodeMsg(mc.HD_LeaderReelectInquiryRsp, rsp, common.RoleNil, []common.Address{target})
}

func (self *controller) sendInquiryRspWithNewBlockReady(repHash common.Hash, target common.Address, number uint64) {
	parentHeader := self.mp.GetParentHeader()
	if parentHeader == nil {
		log.ERROR(self.logInfo, "send<询问响应(新区块已准备完毕响应)>", "获取区块失败")
		return
	}

	rsp := &mc.HD_ReelectInquiryRspMsg{
		Number:    number,
		ReqHash:   repHash,
		Type:      mc.ReelectRSPTypeNewBlockReady,
		AgreeSign: common.Signature{},
		POSResult: nil,
		RLResult:  nil,
		NewBlock:  parentHeader,
	}
	log.ERROR(self.logInfo, "send<询问响应(新区块已准备完毕响应)>", "完成", "block hash", parentHeader.Hash().TerminalString(), "req hash", repHash.TerminalString(), "to", target.Hex())
	self.matrix.HD().SendNodeMsg(mc.HD_LeaderReelectInquiryRsp, rsp, common.RoleNil, []common.Address{target})
}

func (self *controller) sendRLReq() {
	req, err := self.selfCache.GetRLReqMsg()
	if err != nil {
		log.ERROR(self.logInfo, "发送leader重选请求", "获取请求消息失败", "err", err)
		return
	}
	log.INFO(self.logInfo, "send<Leader重选请求>, hash", self.selfCache.rlReqHash.TerminalString(), "轮次", self.curTurnInfo(), "高度", self.Number())
	self.matrix.HD().SendNodeMsg(mc.HD_LeaderReelectReq, req, common.RoleValidator, nil)
}

func (self *controller) sendResultBroadcastMsg() {
	msg, err := self.selfCache.GetResultBroadcastMsg()
	if err != nil {
		log.ERROR(self.logInfo, "发送重选结果广播消息", "获取广播消息失败", "err", err)
		return
	}
	self.matrix.HD().SendNodeMsg(mc.HD_LeaderReelectResultBroadcast, msg, common.RoleValidator, nil)
}

func (self *controller) sendResultBroadcastRsp(req *mc.HD_ReelectResultBroadcastMsg) {
	resultHash := types.RlpHash(req)
	sign, err := self.matrix.SignHelper().SignHashWithValidate(resultHash.Bytes(), true)
	if err != nil {
		log.ERROR(self.logInfo, "响应结果广播消息", "签名失败", "err", err)
		return
	}
	rsp := mc.HD_ReelectResultRspMsg{
		Number:     self.Number(),
		ResultHash: resultHash,
		Sign:       sign,
	}
	self.matrix.HD().SendNodeMsg(mc.HD_LeaderReelectResultBroadcastRsp, rsp, common.RoleNil, []common.Address{req.From})
}

func (self *controller) checkRLReqMsg(req *mc.HD_ReelectLeaderReqMsg) error {
	if nil == req || nil == req.InquiryReq {
		return ErrMsgIsNil
	}
	if req.InquiryReq.ConsensusTurn != self.dc.curConsensusTurn {
		return errors.Errorf("共识轮次不匹配, 消息(%d) != 本地(%d)", req.InquiryReq.ConsensusTurn, self.dc.curConsensusTurn)
	}
	if req.InquiryReq.ReelectTurn != self.dc.curReelectTurn {
		return errors.Errorf("重选轮次不匹配, 消息(%d) != 本地(%d)", req.InquiryReq.ReelectTurn, self.dc.curReelectTurn)
	}
	if req.InquiryReq.Master != req.InquiryReq.From {
		return errors.Errorf("master(%s)和from(%s)不匹配", req.InquiryReq.Master.Hex(), req.InquiryReq.From.Hex())
	}
	if req.InquiryReq.Master != self.dc.GetReelectMaster() {
		return errors.Errorf("master不匹配, master(%s) != 本地master(%s)", req.InquiryReq.Master.Hex(), self.dc.GetReelectMaster().Hex())
	}
	if req.TimeStamp < req.InquiryReq.TimeStamp {
		return errors.Errorf("请求时间戳(%d) < 询问时间戳(%d)", req.TimeStamp, req.InquiryReq.TimeStamp)
	}
	if _, err := self.matrix.DPOSEngine().VerifyHashWithNumber(self.dc, types.RlpHash(req.InquiryReq), req.AgreeSigns, self.Number()); err != nil {
		return errors.Errorf("请求中的询问同意签名POS未通过(%v)", err)
	}

	return nil
}