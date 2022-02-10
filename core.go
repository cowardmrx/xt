package xt

import (
	"errors"
	_ "github.com/go-sql-driver/mysql"
	"github.com/go-xorm/xorm"
	"sync"
)

var (
	clientMap        map[uint]*xorm.Engine // 存储所有的数据库连接
	clientMapLock    sync.Mutex            // 一把锁
	syncModels       []interface{}         // 同步的模型
	syncModelsLock   sync.Mutex            // 一把锁
	autoSyncClient   bool                  // 是否自动同步连接配置
	tenantDBProvider TenantDBProvider      // 租户数据库提供者
	tenantIdResolver TenantIdResolver      // 租户ID解析器
)

func init() {
	clientMap = make(map[uint]*xorm.Engine)
	syncModels = make([]interface{}, 0)
}

// Init 初始化
func Init(p TenantDBProvider, i TenantIdResolver, auto ...bool) error {
	if p == nil {
		return errors.New("db provider is nil")
	}
	tenantDBProvider = p
	if i == nil {
		tenantIdResolver = getTenantId
	} else {
		tenantIdResolver = i
	}
	clients := tenantDBProvider()
	for _, c := range clients {
		if err := Add(c); err != nil {
			return err
		}
	}
	if len(auto) > 0 {
		autoSyncClient = auto[0]
		go autoSyncClientHandle()
	}
	return nil
}

// 自动同步连接配置
func autoSyncClientHandle() {
	for autoSyncClient {
		clients := tenantDBProvider()
		if len(clients) != len(clientMap) {
			for _, c := range clients {
				if err := Add(c); err != nil {
					continue
				}
			}
		}
	}
}

// Add 添加一个数据库连接
func Add(tdb DatabaseClientInfo) error {
	clientMapLock.Lock()
	defer clientMapLock.Unlock()
	// 如果已经存在，则不再添加
	if _, exist := clientMap[tdb.TenantId]; exist {
		return nil
	}
	// 创建数据库连接
	engine, err := xorm.NewEngine("mysql", tdb.GetDSN())
	if err != nil {
		return err
	}
	clientMap[tdb.TenantId] = engine

	// 同步模型
	syncModelsLock.Lock()
	defer syncModelsLock.Unlock()
	for i := range syncModels {
		if err = syncModel(engine, syncModels[i]); err != nil {
			return err
		}
	}
	return nil
}

// GetByTenantId 根据租户Id获取数据库连接对象
func GetByTenantId(tenantId uint) (*xorm.Engine, error) {
	clientMapLock.Lock()
	defer clientMapLock.Unlock()
	if client, exist := clientMap[tenantId]; exist {
		return client, nil
	}
	return nil, errors.New("not found")
}

// AddModel 添加一个需要同步的模型
func AddModel(m interface{}) error {
	// 加把锁
	syncModelsLock.Lock()
	defer syncModelsLock.Unlock()

	syncModels = append(syncModels, m)
	return nil
}

// AddModels 添加一堆需要同步的模型
func AddModels(m ...interface{}) error {
	if len(m) == 0 {
		return nil
	}

	// 加把锁
	syncModelsLock.Lock()
	defer syncModelsLock.Unlock()
	var err error
	for _, v := range m {
		err = AddModel(v)
	}

	return err
}

// 同步模型到数据库
func syncModel(e *xorm.Engine, m interface{}) error {
	if e == nil || m == nil {
		return errors.New("engine or model is nil")
	}
	if err := e.Sync2(m); err != nil {
		return err
	}
	return nil
}
