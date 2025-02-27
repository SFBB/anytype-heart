package localdiscovery

import (
	"context"
	gonet "net"
	"strings"
	"sync"

	"github.com/anyproto/any-sync/accountservice"
	"github.com/anyproto/any-sync/app"
	"github.com/anyproto/any-sync/util/periodicsync"
	"go.uber.org/zap"

	"github.com/anyproto/anytype-heart/core/anytype/config"
	"github.com/anyproto/anytype-heart/net/addrs"
	"github.com/anyproto/anytype-heart/pkg/lib/pb/model"
	"github.com/anyproto/anytype-heart/space/spacecore/clientserver"
)

var notifierProvider NotifierProvider
var proxyLock = sync.Mutex{}

type Hook int

type NotifierProvider interface {
	Provide(notifier Notifier, port int, peerId, serviceName string)
	Remove()
}

func SetNotifierProvider(provider NotifierProvider) {
	// TODO: change to less ad-hoc mechanism and provide default way of injecting components from outside
	proxyLock.Lock()
	defer proxyLock.Unlock()
	notifierProvider = provider
}

func getNotifierProvider() NotifierProvider {
	proxyLock.Lock()
	defer proxyLock.Unlock()
	return notifierProvider
}

type localDiscovery struct {
	peerId string
	port   int

	notifier      Notifier
	drpcServer    clientserver.ClientServer
	manualStart   bool
	periodicCheck periodicsync.PeriodicSync

	hookMu          sync.Mutex
	hookState       DiscoveryPossibility
	hooks           []HookCallback
	networkState    NetworkStateService
	interfacesAddrs addrs.InterfacesAddrs
}

func (l *localDiscovery) PeerDiscovered(ctx context.Context, peer DiscoveredPeer, own OwnAddresses) {
	log.Debug("discovered peer", zap.String("peerId", peer.PeerId), zap.Strings("addrs", peer.Addrs))
	if peer.PeerId == l.peerId {
		return
	}

	var ips []string
	v4addresses, _ := l.getAddresses()
	for _, addr := range v4addresses {
		ip := strings.Split(addr.String(), "/")[0]
		if gonet.ParseIP(ip).To4() != nil {
			ips = append(ips, ip)
		}
	}
	if l.notifier != nil {
		l.notifier.PeerDiscovered(ctx, peer, OwnAddresses{
			Addrs: ips,
			Port:  l.port,
		})
	}
}

func New() LocalDiscovery {
	return &localDiscovery{hooks: make([]HookCallback, 0)}
}

func (l *localDiscovery) SetNotifier(notifier Notifier) {
	l.notifier = notifier
}

func (l *localDiscovery) Init(a *app.App) (err error) {
	l.peerId = a.MustComponent(accountservice.CName).(accountservice.Service).Account().PeerId
	l.drpcServer = a.MustComponent(clientserver.CName).(clientserver.ClientServer)
	l.manualStart = a.MustComponent(config.CName).(*config.Config).DontStartLocalNetworkSyncAutomatically
	l.networkState = app.MustComponent[NetworkStateService](a)
	l.periodicCheck = periodicsync.NewPeriodicSync(5, 0, l.refreshInterfaces, log)

	return
}

func (l *localDiscovery) Run(ctx context.Context) (err error) {
	if l.manualStart {
		// let's wait for the explicit command to enable local discovery
		return
	}

	return l.Start()
}

func (l *localDiscovery) refreshInterfaces(_ context.Context) error {
	newAddrs, err := addrs.GetInterfacesAddrs()
	if err != nil {
		return err
	}
	if addrs.NetAddrsEqualUnordered(newAddrs.Addrs, l.interfacesAddrs.Addrs) {
		return nil
	}

	newAddrs.Interfaces = filterMulticastInterfaces(newAddrs.Interfaces)
	l.interfacesAddrs = newAddrs
	l.discoveryPossibilitySetState(l.getDiscoveryPossibility(newAddrs))
	return nil
}

func (l *localDiscovery) Start() (err error) {
	if !l.drpcServer.ServerStarted() {
		l.discoveryPossibilitySetState(DiscoveryNoInterfaces)
		return
	}
	provider := getNotifierProvider()
	if provider == nil {
		return
	}
	provider.Provide(l, l.drpcServer.Port(), l.peerId, serviceName)
	l.networkState.RegisterHook(func(_ model.DeviceNetworkType) {
		_ = l.refreshInterfaces(context.Background())
	})

	l.port = l.drpcServer.Port()
	l.periodicCheck.Run()
	return
}

func (l *localDiscovery) Name() (name string) {
	return CName
}

func (l *localDiscovery) Close(ctx context.Context) (err error) {
	if !l.drpcServer.ServerStarted() {
		return
	}
	l.periodicCheck.Close()
	provider := getNotifierProvider()
	if provider == nil {
		return
	}
	provider.Remove()
	return nil
}
