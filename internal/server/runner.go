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
	ctx context.Context

	nodeID   string
	data     *data.Data
	election *concurrency.Election

	// Event channel for election events (only consumed by eventLoop)
	eventCh chan ElectionEvent

	// Channel to notify campaignLoop to start campaigning
	campaignCh chan struct{}

	logger *slog.Logger
}

func NewServer(ctx context.Context, data *data.Data, nodeID string) (*Server, error) {
	srv := &Server{
		ctx:        ctx,
		data:       data,
		nodeID:     nodeID,
		eventCh:    make(chan ElectionEvent, 10),
		campaignCh: make(chan struct{}, 1),
	}
	srv.logger = slog.Default().With(slog.String("node-id", nodeID))
	return srv, nil
}

func (s *Server) Run() error {
	s.logger.Info("server running...")

	return s.electionloop(s.ctx)
}

func (s *Server) electionloop(ctx context.Context) error {
	logger := s.logger.With(slog.String("loop", "electionloop"))

	logger.Debug("electionloop running...")
	defer logger.Debug("electionloop stopped")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			logger.Debug("start elect")
			if err := s.elect(ctx); err != nil {
				logger.Error("elect failed", "err", err)
				time.Sleep(time.Millisecond * 100)
			}
		}
	}
}

func (s *Server) elect(ctx context.Context) error {
	session, err := concurrency.NewSession(
		s.data.Etcd,
		concurrency.WithContext(ctx),
		concurrency.WithTTL(15),
	)
	if err != nil {
		return err
	}
	defer session.Close()

	s.election = concurrency.NewElection(session, "/election")

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		s.eventLoop(session.Ctx())
	}()
	go func() {
		defer wg.Done()
		s.watchLoop(session.Ctx())
	}()
	go func() {
		defer wg.Done()
		s.campaignLoop(session.Ctx())
	}()

	wg.Wait()

	return ctx.Err()
}

// watchLoop: 负责监听 leader 变化
func (s *Server) watchLoop(ctx context.Context) {
	logger := s.logger.With(slog.String("loop", "watchloop"))

	logger.Debug("watchloop running...")
	defer logger.Debug("watchloop stopped")

	// 直接 watch election prefix，监听所有变化
	watchCh := s.data.Etcd.Watch(ctx, "/election/", clientv3.WithPrefix())

	for {
		logger.Debug("watch loop running...")
		select {
		case <-ctx.Done():
			return

		case wr, ok := <-watchCh:
			if !ok {
				logger.Info("watch channel closed")
				return
			}

			if wr.Err() != nil {
				logger.Error("watch error", "error", wr.Err())
				return
			}

			for _, ev := range wr.Events {
				logger.Debug("watch event",
					"type", ev.Type.String(),
					"key", string(ev.Kv.Key))

				var evt ElectionEvent
				switch ev.Type {
				case mvccpb.PUT:
					// 新 leader 当选
					newLeader := string(ev.Kv.Value)
					logger.Debug("leader elected/changed",
						"new", newLeader)
					evt = ElectionEvent{
						Type:   LeaderChanged,
						Leader: newLeader,
					}

				case mvccpb.DELETE:
					// Leader 消失（resign 或 session 过期）
					logger.Debug("leader key deleted", "key", string(ev.Kv.Key))
					evt = ElectionEvent{Type: LeaderGone}

				}

				select {
				case <-ctx.Done():
					return
				case s.eventCh <- evt:
				}
			}
		}
	}
}

// Campaign Loop: 负责竞选
func (s *Server) campaignLoop(ctx context.Context) {
	logger := s.logger.With(slog.String("loop", "campaignloop"))

	logger.Debug("campaignloop running...")
	defer logger.Debug("campaignloop stopped")

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
		select {
		case <-ctx.Done():
			return

		case <-s.campaignCh:
			// 收到竞选触发信号（事件驱动）
			logger.Debug("campaign triggered by event")

			go s.campaign(ctx)
		}
	}
}

// campaign 同时竞选和观察当前 leader
func (s *Server) campaign(ctx context.Context) {
	s.logger.Debug("leadership checking")

	// 先快速检查当前是否有 leader
	resp, err := s.election.Leader(s.ctx)

	if err == nil {
		if len(resp.Kvs) > 0 {
			currentLeader := string(resp.Kvs[0].Value)
			s.logger.Debug("current leader exists", "leader", currentLeader)
			evt := ElectionEvent{
				Type:   LeaderElected,
				Leader: currentLeader,
			}

			select {
			case <-ctx.Done():
				return
			case s.eventCh <- evt:
			}

		}
		return
	}

	if err != concurrency.ErrElectionNoLeader {
		s.logger.Error("failed to check current leader", "error", err)
		return
	}

	// 没有当前 leader，开始竞选
	s.logger.Debug("no leader, start campaigning")

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
				s.logger.Error("campaign failed", "error", err)
				return
			}
			if err == nil {
				// 成功成为 leader
				s.logger.Debug("campaign success")
				evt := ElectionEvent{
					Type:   LeaderElected,
					Leader: s.nodeID,
				}
				select {
				case <-ctx.Done():
					return
				case s.eventCh <- evt:
				}
			}
			return

		case resp, ok := <-observeCh:
			if !ok {
				s.logger.Debug("observe channel closed")
				return
			}

			// etcd Observe() sends []*mvccpb.KeyValue{nil} when leader is gone
			if len(resp.Kvs) > 0 && resp.Kvs[0] != nil {
				currentLeader := string(resp.Kvs[0].Value)

				var evt ElectionEvent
				if currentLeader == s.nodeID {
					// 自己成为了 leader，取消竞选 goroutine
					cancel()
					s.logger.Debug("became leader")
					evt = ElectionEvent{
						Type:   LeaderElected,
						Leader: s.nodeID,
					}

				} else {
					// 竞选期间有其他节点抢先成为 leader
					cancel()
					s.logger.Debug("observed leader during campaign", "leader", currentLeader)
					evt = ElectionEvent{
						Type:   LeaderElected,
						Leader: currentLeader,
					}

				}

				select {
				case <-ctx.Done():
					return
				case s.eventCh <- evt:
				}
				return
			}
		}
	}
}

// eventLoop: 处理事件和业务逻辑切换
func (s *Server) eventLoop(ctx context.Context) {
	var (
		currentRole   Role = RoleUnknown
		currentLeader string
		roleCtx       context.Context
		roleCancel    context.CancelFunc
		logger        *slog.Logger = s.logger.With(slog.String("loop", "eventloop"))
	)

	logger.Debug("running...")
	defer logger.Debug("stopped")

	regisnLeadership := func() {
		if currentRole == RoleLeader {
			logger.Debug("resigning leadership")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := s.election.Resign(ctx); err != nil {
				logger.Error("failed to resign", "error", err)
			} else {
				logger.Debug("resign leadership success")
			}
		}
	}

	defer regisnLeadership()

	stopCurrentRole := func() {
		if roleCancel != nil {
			logger.Debug("stopping current role", "role", currentRole)
			roleCancel()
			roleCancel = nil
			roleCtx = nil
		}
	}
	startNewRole := func(newRole Role) {
		roleCtx, roleCancel = context.WithCancel(s.ctx)

		switch newRole {
		case RoleLeader:
			go s.leaderLoop(roleCtx)
		case RoleFollower:
			go s.followerLoop(roleCtx)
		}

		currentRole = newRole
	}

	for {
		select {
		case <-ctx.Done():
			stopCurrentRole()
			return

		case evt := <-s.eventCh:
			logger.Debug("got event",
				"event", evt.Type.String(),
				"leader", evt.Leader,
			)

			var newRole Role

			switch evt.Type {
			case LeaderElected, LeaderChanged:
				if currentLeader == evt.Leader {
					continue
				}

				logger.Debug("leader changing",
					"old", currentLeader,
					"new", evt.Leader,
				)
				currentLeader = evt.Leader
				if currentLeader == s.nodeID {
					newRole = RoleLeader
				} else {
					newRole = RoleFollower
				}

			case LeaderGone:
				// Leader 消失，通知 campaignLoop 开始竞选
				currentLeader = ""
				select {
				case <-ctx.Done():
					return
				case s.campaignCh <- struct{}{}:
					logger.Debug("try campaign")
				}
			}

			if newRole != currentRole {
				logger.Debug("role changing", "from", currentRole, "to", newRole)
				stopCurrentRole()
				startNewRole(newRole)
			}
		}
	}
}

// Leader Loop: Leader 的业务逻辑
func (s *Server) leaderLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("leader loop stopped")
			return

		case <-ticker.C:
			s.logger.Info("leader working...")

			// Leader specific work here
			resp, err := s.data.Etcd.Get(ctx, "foo")
			if err != nil {
				s.logger.Error("leader get failed", "error", err)
				continue
			}

			if len(resp.Kvs) > 0 {
				s.logger.Info("leader: data found",
					"key", string(resp.Kvs[0].Key),
					"value", string(resp.Kvs[0].Value))
			}
		}
	}
}

// Follower Loop: Follower 的业务逻辑
func (s *Server) followerLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("follower loop stopped")
			return

		case <-ticker.C:
			s.logger.Info("follower working...")

			// Follower specific work here
			resp, err := s.data.Etcd.Get(ctx, "foo")
			if err != nil {
				s.logger.Error("follower get failed", "error", err)
				continue
			}

			if len(resp.Kvs) > 0 {
				s.logger.Info("follower: data found",
					"key", string(resp.Kvs[0].Key),
					"value", string(resp.Kvs[0].Value))
			}
		}
	}
}
