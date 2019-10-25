// Copyright 2019, Chef.  All rights reserved.
// https://github.com/q191201771/lal
//
// Use of this source code is governed by a MIT-style license
// that can be found in the License file.
//
// Author: Chef (191201771@qq.com)

package rtmp_test

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/q191201771/lal/pkg/httpflv"
	"github.com/q191201771/lal/pkg/logic"
	"github.com/q191201771/lal/pkg/rtmp"
	"github.com/q191201771/naza/pkg/assert"
	log "github.com/q191201771/naza/pkg/nazalog"
)

var (
	serverAddr = ":10001"
	pushURL    = "rtmp://127.0.0.1:10001/live/test"
	pullURL    = "rtmp://127.0.0.1:10001/live/test"
	rFLVFile   = "testdata/test.flv"
	wFLVFile   = "testdata/out.flv"
	wgNum      = 4 // FLVFileReader -> [push -> pub -> sub -> pull] -> FLVFileWriter
)

var (
	pubSessionObs MockPubSessionObserver
	subSession    *rtmp.ServerSession
	wg            sync.WaitGroup
	w             httpflv.FLVFileWriter
	//
	rc uint32
	bc uint32
	wc uint32
)

type MockServerObserver struct {
}

func (so *MockServerObserver) NewRTMPPubSessionCB(session *rtmp.ServerSession) bool {
	log.Debug("NewRTMPPubSessionCB")
	session.SetPubSessionObserver(&pubSessionObs)
	return true
}
func (so *MockServerObserver) NewRTMPSubSessionCB(session *rtmp.ServerSession) bool {
	log.Debug("NewRTMPSubSessionCB")
	subSession = session
	return true
}
func (so *MockServerObserver) DelRTMPPubSessionCB(session *rtmp.ServerSession) {
	log.Debug("DelRTMPPubSessionCB")
	subSession.Flush()
	subSession.Dispose()
	wg.Done()
}
func (so *MockServerObserver) DelRTMPSubSessionCB(session *rtmp.ServerSession) {
	log.Debug("DelRTMPSubSessionCB")
	wg.Done()
}

type MockPubSessionObserver struct {
}

func (pso *MockPubSessionObserver) ReadRTMPAVMsgCB(header rtmp.Header, timestampAbs uint32, message []byte) {
	bc++
	// 转发
	var currHeader rtmp.Header
	currHeader.MsgLen = uint32(len(message))
	currHeader.Timestamp = timestampAbs
	currHeader.MsgTypeID = header.MsgTypeID
	currHeader.MsgStreamID = rtmp.MSID1
	switch header.MsgTypeID {
	case rtmp.TypeidDataMessageAMF0:
		currHeader.CSID = rtmp.CSIDAMF
	case rtmp.TypeidAudio:
		currHeader.CSID = rtmp.CSIDAudio
	case rtmp.TypeidVideo:
		currHeader.CSID = rtmp.CSIDVideo
	}
	var absChunks []byte
	absChunks = rtmp.Message2Chunks(message, &currHeader)
	subSession.AsyncWrite(absChunks)
}

type MockPullSessionObserver struct {
}

func (pso *MockPullSessionObserver) ReadRTMPAVMsgCB(header rtmp.Header, timestampAbs uint32, message []byte) {
	tag := logic.Trans.RTMPMsg2FLVTag(header, timestampAbs, message)
	w.WriteTag(*tag)
	atomic.AddUint32(&wc, 1)
}

func TestExample(t *testing.T) {
	var err error

	var r httpflv.FLVFileReader
	err = r.Open(rFLVFile)
	if err != nil {
		return
	}

	wg.Add(wgNum)

	var so MockServerObserver
	s := rtmp.NewServer(&so, serverAddr)
	go s.RunLoop()

	// 等待 server 开始监听
	time.Sleep(100 * time.Millisecond)

	var pso MockPullSessionObserver
	pullSession := rtmp.NewPullSession(&pso, rtmp.PullSessionTimeout{})
	log.Debug("tag1")
	err = pullSession.Pull(pullURL)
	assert.Equal(t, nil, err)
	log.Debugf("tag2, %v", err)

	pushSession := rtmp.NewPushSession(rtmp.PushSessionTimeout{})
	err = pushSession.Push(pushURL)
	assert.Equal(t, nil, err)

	err = w.Open(wFLVFile)
	assert.Equal(t, nil, err)
	err = w.WriteRaw(httpflv.FLVHeader)
	assert.Equal(t, nil, err)

	_, err = r.ReadFLVHeader()
	assert.Equal(t, nil, err)
	for {
		tag, err := r.ReadTag()
		if err == io.EOF {
			break
		}
		assert.Equal(t, nil, err)
		rc++
		h, _, m := logic.Trans.FLVTag2RTMPMsg(*tag)
		chunks := rtmp.Message2Chunks(m, &h)
		err = pushSession.AsyncWrite(chunks)
		assert.Equal(t, nil, err)
	}

	r.Dispose()
	wg.Done()

	err = pushSession.Flush()
	assert.Equal(t, nil, err)
	pushSession.Dispose()
	wg.Done()

	wg.Wait()

	// 等待 pull 完成
	for {
		if atomic.LoadUint32(&wc) == rc {
			break
		}
		time.Sleep(1 * time.Nanosecond)
	}
	//time.Sleep(1 * time.Second)

	pullSession.Dispose()
	w.Dispose()

	s.Dispose()
	log.Debugf("rc=%d, bc=%d, wc=%d", rc, bc, atomic.LoadUint32(&wc))
	compareFile(t)
}

func compareFile(t *testing.T) {
	r, err := ioutil.ReadFile(rFLVFile)
	assert.Equal(t, nil, err)
	w, err := ioutil.ReadFile(wFLVFile)
	assert.Equal(t, nil, err)
	res := bytes.Compare(r, w)
	assert.Equal(t, 0, res)
	err = os.Remove(wFLVFile)
	assert.Equal(t, nil, err)
}