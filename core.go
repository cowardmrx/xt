package xt

import (
	"errors"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

var (
	clientMap        map[uint]*gorm.DB   // 存储所有的数据库连接
	clientInfoMap    map[uint]TenantInfo // 租户信息
	clientMapLock    sync.Mutex          // 一把锁
	syncModels       []interface{}       // 同步的模型
	syncModelsLock   sync.Mutex          // 一把锁
	autoSyncClient   bool                // 是否自动同步连接配置
	tenantDBProvider TenantDBProvider    // 租户数据库提供者
	tenantIdResolver TenantIdResolver    // 租户ID解析器
	logs             io.Writer           // 日志输出
)

func init() {
	clientMap = make(map[uint]*gorm.DB)
	clientInfoMap = make(map[uint]TenantInfo)
	syncModels = make([]interface{}, 0)
	logs = os.Stdout
}

// SetLogger 设置日志输出工具
func SetLogger(out io.Writer) {
	logs = out
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
		// 休眠五分钟再来
		time.Sleep(time.Minute * 5)
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
	gl := logger.New(
		log.New(logs, "", log.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Second, // Slow SQL threshold
			IgnoreRecordNotFoundError: false,       // 忽略没找到结果的错误
			LogLevel:                  logger.Info, // Log level
			Colorful:                  false,       // Disable color
		},
	)
	engine, err := gorm.Open(mysql.Open(tdb.GetDSN()), &gorm.Config{Logger: gl})
	if err != nil {
		return err
	}
	clientMap[tdb.TenantId] = engine
	clientInfoMap[tdb.TenantId] = tdb.Info

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
func GetByTenantId(tenantId uint) (*gorm.DB, error) {
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
func syncModel(e *gorm.DB, m interface{}) error {
	if e == nil || m == nil {
		return errors.New("engine or model is nil")
	}
	if err := e.AutoMigrate(m); err != nil {
		return err
	}
	return nil
}
