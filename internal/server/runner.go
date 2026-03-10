package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"zen/internal/data"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

type Role string

const (
	RoleUnknown  Role = "unknown"
	RoleLeader   Role = "leader"
	RoleFollower Role = "follower"
)

type EventType int

const (
	LeaderElected EventType = iota
	LeaderChanged
	LeaderGone
)

func (e EventType) String() string {
	switch e {
	case LeaderElected:
		return "LeaderElected"
	case LeaderChanged:
		return "LeaderChanged"
	case LeaderGone:
		return "LeaderGone"
	default:
		return ""
	}
}

type ElectionEvent struct {
	Type   EventType
	Leader string
}

type Server struct {
	ctx    context.Context
	data   *data.Data
	nodeID string

	mu            sync.RWMutex
	currentRole   Role
	roleCtx       context.Context
	roleCancel    context.CancelFunc
	election      *concurrency.Election
	currentLeader string

	// Event channel for election events (only consumed by eventLoop)
	eventCh chan ElectionEvent

	// Channel to notify campaignLoop to start campaigning
	campaignCh chan struct{}
}

func NewServer(ctx context.Context, data *data.Data, nodeID string) (*Server, error) {
	return &Server{
		ctx:           ctx,
		data:          data,
		nodeID:        nodeID,
		currentRole:   RoleUnknown,
		eventCh:       make(chan ElectionEvent, 10),
		campaignCh:    make(chan struct{}, 1),
		currentLeader: "",
	}, nil
}

func (s *Server) Run() error {
	for {
		select {
		case <-s.ctx.Done():
			slog.Info("server exited")
			return nil
		default:
			if err := s.run(s.ctx); err != nil {
				slog.Error("session run failed", "err", err)
				time.Sleep(time.Millisecond * 100)
			}
		}
	}
}

func (s *Server) run(ctx context.Context) error {
	session, err := concurrency.NewSession(s.data.Etcd,
		concurrency.WithContext(ctx),
		concurrency.WithTTL(15),
	)
	if err != nil {
		return err
	}
	defer session.Close()

	s.election = concurrency.NewElection(session, "/election")

	var wg sync.WaitGroup

	wg.Go(func() {
		s.eventLoop(ctx)
	})
	wg.Go(func() {
		s.watchLoop(ctx)
	})
	wg.Go(func() {
		s.campaignLoop(ctx)
	})

	wg.Wait()

	slog.Info("session closed, stopping all loops")

	return ctx.Err()
}

// watchLoop: 负责监听 leader 变化
func (s *Server) watchLoop(ctx context.Context) {
	slog.Info("starting watch loop")
	defer slog.Info("watch loop stopped")

	// 直接 watch election prefix，监听所有变化
	watchCh := s.data.Etcd.Watch(ctx, "/election/", clientv3.WithPrefix())

	for {
		slog.Debug("watch loop running...")
		select {
		case <-ctx.Done():
			slog.Info("watch loop received ctx.Done")
			return

		case wr, ok := <-watchCh:
			if !ok {
				slog.Info("watch channel closed")
				return
			}

			if wr.Err() != nil {
				slog.Error("watch error", "error", wr.Err())
				return
			}

			for _, ev := range wr.Events {
				slog.Debug("watch event",
					"type", ev.Type.String(),
					"key", string(ev.Kv.Key))

				s.mu.Lock()
				switch ev.Type {
				case mvccpb.PUT:
					// 新 leader 当选
					newLeader := string(ev.Kv.Value)
					slog.Debug("leader elected/changed",
						"new", newLeader)
					s.eventCh <- ElectionEvent{
						Type:   LeaderChanged,
						Leader: newLeader,
					}

				case mvccpb.DELETE:
					// Leader 消失（resign 或 session 过期）
					slog.Debug("leader key deleted", "key", string(ev.Kv.Key))
					s.eventCh <- ElectionEvent{Type: LeaderGone}
				}
				s.mu.Unlock()
			}
		}
	}
}

// 2. Campaign Loop: 只负责竞选
func (s *Server) campaignLoop(ctx context.Context) {
	slog.Info("starting campaign loop")

	cleanup := func() {
		slog.Info("campaign loop stopping")
		s.mu.RLock()
		isLeader := (s.currentRole == RoleLeader)
		s.mu.RUnlock()

		if isLeader {
			slog.Info("resigning leadership")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := s.election.Resign(ctx); err != nil {
				slog.Error("failed to resign", "error", err)
			}
		}

		slog.Info("campaign loop stopped")
	}

	defer cleanup()

	// 定时检查兜底机制（每 30 秒检查一次）
	go func() {
		select {
		case <-ctx.Done():
			return
		case s.campaignCh <- struct{}{}:
			// 初始触发
		}

		checkInterval := 30 * time.Second
		checkTimer := time.NewTicker(checkInterval)
		defer checkTimer.Stop()

		for range checkTimer.C {
			select {
			case <-ctx.Done():
				return
			case s.campaignCh <- struct{}{}:
			default:
			}
		}
	}()

	for {
		slog.Debug("campaign loop running...")
		select {
		case <-ctx.Done():
			return

		case <-s.campaignCh:
			// 收到竞选触发信号（事件驱动）
			slog.Debug("campaign triggered by event")

			go s.campaign()
		}
	}
}

// campaign 同时竞选和观察当前 leader
func (s *Server) campaign() {
	slog.Debug("campaigning for leadership", "nodeID", s.nodeID)

	// 先快速检查当前是否有 leader
	resp, err := s.election.Leader(s.ctx)

	if err == nil {
		if len(resp.Kvs) > 0 {
			currentLeader := string(resp.Kvs[0].Value)
			slog.Debug("current leader exists", "leader", currentLeader)
			s.eventCh <- ElectionEvent{
				Type:   LeaderElected,
				Leader: currentLeader,
			}
		}
		return
	}

	if err != concurrency.ErrElectionNoLeader {
		slog.Error("failed to check current leader", "error", err)
		return
	}

	// 没有当前 leader，开始竞选
	slog.Debug("no current leader, starting campaign")

	// 创建一个 context 来控制竞选
	campaignCtx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	// 启动竞选 goroutine
	campaignCh := make(chan error, 1)
	go func() {
		campaignCh <- s.election.Campaign(campaignCtx, s.nodeID)
	}()

	// 同时观察当前 leader（防止竞选期间有其他节点抢先）
	observeCh := s.election.Observe(campaignCtx)

	for {
		select {
		case err := <-campaignCh:
			if err != nil && err != context.Canceled {
				slog.Error("campaign failed", "error", err)
				return
			}
			if err == nil {
				// 成功成为 leader
				slog.Debug("became leader", "nodeID", s.nodeID)
				s.eventCh <- ElectionEvent{
					Type:   LeaderElected,
					Leader: s.nodeID,
				}
			}
			return

		case resp, ok := <-observeCh:
			if !ok {
				slog.Debug("observe channel closed")
				return
			}

			// etcd Observe() sends []*mvccpb.KeyValue{nil} when leader is gone
			if len(resp.Kvs) > 0 && resp.Kvs[0] != nil {
				currentLeader := string(resp.Kvs[0].Value)
				slog.Debug("observed current leader during campaign", "leader", currentLeader)

				if currentLeader == s.nodeID {
					// 自己成为了 leader，取消竞选 goroutine
					cancel()
					slog.Debug("became leader", "nodeID", s.nodeID)
					s.eventCh <- ElectionEvent{
						Type:   LeaderElected,
						Leader: s.nodeID,
					}
					return
				} else {
					// 竞选期间有其他节点抢先成为 leader
					cancel()
					slog.Debug("other node became leader during campaign", "leader", currentLeader)
					s.eventCh <- ElectionEvent{
						Type:   LeaderElected,
						Leader: currentLeader,
					}
					return
				}
			}
		}
	}
}

// eventLoop: 处理事件和业务逻辑切换
func (s *Server) eventLoop(ctx context.Context) {
	slog.Info("starting event loop")
	defer slog.Info("event loop stopped")

	for {
		slog.Debug("event loop running...")
		select {
		case <-ctx.Done():
			s.stopCurrentRole()
			return

		case event := <-s.eventCh:
			s.handleEvent(event)
		}
	}
}

func (s *Server) handleEvent(event ElectionEvent) {
	slog.Debug("eventloop got event",
		"event", event.Type.String(),
		"leader", event.Leader,
	)
	s.mu.Lock()
	defer s.mu.Unlock()

	var newRole Role

	switch event.Type {
	case LeaderElected, LeaderChanged:
		slog.Debug("leader changing",
			"old", s.currentLeader,
			"new", event.Leader,
		)
		s.currentLeader = event.Leader
		if event.Leader == s.nodeID {
			newRole = RoleLeader
		} else {
			newRole = RoleFollower
		}

	case LeaderGone:
		// Leader 消失，通知 campaignLoop 开始竞选
		s.currentLeader = ""
		slog.Debug("leader gone, triggering campaign")
		select {
		case s.campaignCh <- struct{}{}:
			slog.Debug("event loop trigger campaign success")
		default:
		}
		return
	}

	if newRole != s.currentRole {
		slog.Debug("role changing", "from", s.currentRole, "to", newRole)
		s.switchRole(newRole)
	}
}

func (s *Server) switchRole(newRole Role) {
	// 停止当前角色
	s.stopCurrentRole()

	// 启动新角色
	s.currentRole = newRole
	s.roleCtx, s.roleCancel = context.WithCancel(s.ctx)

	switch newRole {
	case RoleLeader:
		go s.leaderLoop(s.roleCtx)
	case RoleFollower:
		go s.followerLoop(s.roleCtx)
	}
}

func (s *Server) stopCurrentRole() {
	if s.roleCancel != nil {
		slog.Debug("stopping current role", "role", s.currentRole)
		s.roleCancel()
		s.roleCancel = nil
		s.roleCtx = nil
	}
}

// Leader Loop: Leader 的业务逻辑
func (s *Server) leaderLoop(ctx context.Context) {
	slog.Info("starting leader loop")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("leader loop stopped")
			return

		case <-ticker.C:
			slog.Info("leader working...")

			// Leader specific work here
			resp, err := s.data.Etcd.Get(ctx, "foo")
			if err != nil {
				slog.Error("leader get failed", "error", err)
				continue
			}

			if len(resp.Kvs) > 0 {
				slog.Info("leader: data found",
					"key", string(resp.Kvs[0].Key),
					"value", string(resp.Kvs[0].Value))
			}
		}
	}
}

// Follower Loop: Follower 的业务逻辑
func (s *Server) followerLoop(ctx context.Context) {
	slog.Info("starting follower loop")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("follower loop stopped")
			return

		case <-ticker.C:
			slog.Info("follower working...")

			// Follower specific work here
			resp, err := s.data.Etcd.Get(ctx, "foo")
			if err != nil {
				slog.Error("follower get failed", "error", err)
				continue
			}

			if len(resp.Kvs) > 0 {
				slog.Info("follower: data found",
					"key", string(resp.Kvs[0].Key),
					"value", string(resp.Kvs[0].Value))
			}
		}
	}
}
