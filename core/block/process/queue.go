package process

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/cheggaaa/mb/v3"
	"github.com/globalsign/mgo/bson"

	"github.com/anyproto/anytype-heart/pb"
	"github.com/anyproto/anytype-heart/pkg/lib/pb/model"
)

var (
	ErrQueueDone           = errors.New("queue done")
	ErrQueueCanceled       = errors.New("queue canceled")
	ErrQueueAlreadyStarted = errors.New("queue already started")
	ErrQueueNotStarted     = errors.New("queue not started")
)

type Task func()

type Queue interface {
	Process
	Notificationable
	// Start starts the queue and register process in service
	Start() (err error)
	// Add adds tasks to queue. Can be called before Start
	Add(t ...Task) (err error)
	// Wait adds tasks to queue and wait for done. Can be called before Start
	Wait(t ...Task) (err error)
	// SetMessage sets progress message
	SetMessage(msg string)
	// Finalize must be called after all tasks was added. Will wait for all tasks complete
	Finalize() (err error)
	// Stop stops the queue with given error (can be nil)
	Stop(err error)
}

func (s *service) NewQueue(info pb.ModelProcess, workers int, noProgress bool, notificationService NotificationService) Queue {
	if workers <= 0 {
		workers = 1
	}
	if info.Id == "" {
		info.Id = bson.NewObjectId().Hex()
	}
	q := &queue{
		id:                  info.Id,
		info:                info,
		state:               pb.ModelProcess_None,
		msgs:                mb.New[Task](0),
		done:                make(chan struct{}),
		cancel:              make(chan struct{}),
		s:                   s,
		workers:             workers,
		wg:                  &sync.WaitGroup{},
		noProgress:          noProgress,
		notificationService: notificationService,
	}
	q.wg.Add(workers)
	return q
}

type queue struct {
	id                  string
	info                pb.ModelProcess
	state               pb.ModelProcessState
	msgs                *mb.MB[Task]
	wg                  *sync.WaitGroup
	done, cancel        chan struct{}
	pTotal, pDone       int64
	workers             int
	s                   Service
	m                   sync.Mutex
	message             string
	noProgress          bool
	notificationService NotificationService
	notification        *model.Notification
}

func (p *queue) Id() string {
	return p.id
}

func (p *queue) Start() (err error) {
	p.m.Lock()
	defer p.m.Unlock()
	if p.state != pb.ModelProcess_None {
		return ErrQueueAlreadyStarted
	}
	p.state = pb.ModelProcess_Running
	for i := 0; i < p.workers; i++ {
		go p.worker()
	}
	if p.noProgress {
		return nil
	}
	return p.s.Add(p)
}

func (p *queue) Add(ts ...Task) (err error) {
	p.m.Lock()
	defer p.m.Unlock()
	if err = p.checkRunning(false); err != nil {
		return
	}
	for _, t := range ts {
		if err = p.msgs.Add(context.Background(), t); err != nil {
			return ErrQueueDone
		}
		atomic.AddInt64(&p.pTotal, 1)
	}
	return nil
}

func (p *queue) Wait(ts ...Task) (err error) {
	p.m.Lock()
	if err = p.checkRunning(false); err != nil {
		p.m.Unlock()
		return
	}
	p.m.Unlock()
	var done = make(chan struct{}, len(ts))
	for _, t := range ts {
		if err = p.msgs.Add(context.Background(), taskFunction(t, done)); err != nil {
			return ErrQueueDone
		}
		atomic.AddInt64(&p.pTotal, 1)
	}
	for _ = range ts {
		select {
		case <-p.cancel:
			return ErrQueueCanceled
		case <-p.done:
			return ErrQueueDone
		case <-done:
		}
	}
	return
}

func taskFunction(t Task, done chan struct{}) func() {
	return func() {
		t()
		done <- struct{}{}
	}
}

func (p *queue) Finalize() (err error) {
	p.m.Lock()
	if err = p.checkRunning(true); err != nil {
		p.m.Unlock()
		return err
	}
	if err = p.msgs.Close(); err != nil {
		p.m.Unlock()
		return ErrQueueDone
	}
	p.state = pb.ModelProcess_Done

	p.m.Unlock()
	p.wg.Wait()
	close(p.done)
	return
}

func (p *queue) Cancel() (err error) {
	p.m.Lock()
	if err = p.checkRunning(true); err != nil {
		p.m.Unlock()
		return err
	}
	close(p.cancel)
	// flush queue
	p.msgs.Pause()
	p.state = pb.ModelProcess_Canceled
	if err = p.msgs.Close(); err != nil {
		p.m.Unlock()
		return ErrQueueDone
	}
	p.m.Unlock()
	p.wg.Wait()
	close(p.done)
	return
}

func (p *queue) Info() pb.ModelProcess {
	p.m.Lock()
	defer p.m.Unlock()
	return pb.ModelProcess{
		Id:    p.id,
		State: p.state,
		Progress: &pb.ModelProcessProgress{
			Total:   atomic.LoadInt64(&p.pTotal),
			Done:    atomic.LoadInt64(&p.pDone),
			Message: p.message,
		},
		Message: p.info.Message,
	}
}

func (p *queue) Done() chan struct{} {
	return p.done
}

func (p *queue) SetMessage(msg string) {
	p.m.Lock()
	p.message = msg
	p.m.Unlock()
}

func (p *queue) Stop(err error) {
	p.m.Lock()
	if e := p.checkRunning(true); e != nil {
		p.m.Unlock()
		return
	}
	close(p.cancel)
	// flush queue
	p.msgs.Pause()
	if closeErr := p.msgs.Close(); closeErr != nil {
		p.m.Unlock()
		return
	}
	if err == nil {
		p.state = pb.ModelProcess_Done
	} else {
		p.state = pb.ModelProcess_Error
		p.message = err.Error()
	}

	p.m.Unlock()
	p.wg.Wait()
	close(p.done)
	return
}

func (p *queue) FinishWithNotification(notification *model.Notification, err error) {
	p.notification = notification
}

func (p *queue) SendNotification() {
	if p.notification == nil {
		return
	}
	err := p.notificationService.CreateAndSend(p.notification)
	if err != nil {
		log.Errorf("failed to send notification: %v", err)
	}
}

func (p *queue) checkRunning(checkStarted bool) (err error) {
	if checkStarted && p.state == pb.ModelProcess_None {
		return ErrQueueNotStarted
	}
	switch p.state {
	case pb.ModelProcess_Canceled:
		return ErrQueueCanceled
	case pb.ModelProcess_Done:
		return ErrQueueDone
	default:
		return nil
	}
}

func (p *queue) worker() {
	defer p.wg.Done()
	for {
		msgs, err := p.msgs.NewCond().WithMax(1).Wait(context.Background())
		if err != nil {
			log.Errorf("failed wait: %v", err)
			return
		}
		if len(msgs) == 0 {
			return
		}
		msgs[0]()
		atomic.AddInt64(&p.pDone, 1)
	}
}
