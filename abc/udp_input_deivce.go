package abc

import (
	"context"
	"github.com/parkingwang/go-conf"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/yoojia/go-gecko"
	"net"
	"time"
)

func UdpInputDeviceFactory() (string, gecko.BundleFactory) {
	return "UdpInputDevice", func() interface{} {
		return NewUdpInputDevice()
	}
}

func NewUdpInputDevice() *UdpInputDevice {
	return &UdpInputDevice{
		AbcInputDevice: gecko.NewAbcInputDevice(),
	}
}

// UDP服务器读取设备
type UdpInputDevice struct {
	*gecko.AbcInputDevice
	maxBufferSize  int64
	readTimeout    time.Duration
	cancelCtx      context.Context
	cancelFun      context.CancelFunc
	onServeHandler func(bytes []byte, ctx gecko.Context, deliverer gecko.Deliverer) error
	topic          string
}

func (ur *UdpInputDevice) OnInit(args map[string]interface{}, ctx gecko.Context) {
	config := conf.WrapImmutableMap(args)
	ur.maxBufferSize = config.GetInt64OrDefault("bufferSizeKB", 1) * 1024
	ur.readTimeout = config.GetDurationOrDefault("readTimeout", time.Second*10)
	ur.topic = config.MustString("topic")
}

func (ur *UdpInputDevice) OnStart(ctx gecko.Context) {
	address := ur.GetUnionAddress()
	ur.withTag(log.Info).Msgf("使用UDP服务端模式，绑定地址： %s", address)
	ur.cancelCtx, ur.cancelFun = context.WithCancel(context.Background())
	if nil == ur.onServeHandler {
		ur.withTag(log.Warn).Msg("使用默认数据处理接口")
		if "" == ur.topic {
			ur.withTag(log.Panic).Msg("使用默认接口必须设置topic参数")
		}
		ur.onServeHandler = func(bytes []byte, ctx gecko.Context, deliverer gecko.Deliverer) error {
			return deliverer.DeliverPublish(ur.topic, gecko.PacketFrame(bytes))
		}
	}
}

func (ur *UdpInputDevice) OnStop(ctx gecko.Context) {
	ur.cancelFun()
}

func (ur *UdpInputDevice) Serve(ctx gecko.Context, deliverer gecko.Deliverer) error {
	if nil == ur.onServeHandler {
		return errors.New("未设置onServeHandler接口")
	}
	address := ur.GetUnionAddress()
	conn, cErr := net.ListenPacket("udp", address)
	if nil != cErr {
		return cErr
	}
	ur.withTag(log.Info).Msgf("监听UDP服务： %s", address)
	defer conn.Close()

	isNetTempErr := func(err error) bool {
		if nErr, ok := err.(net.Error); ok {
			return nErr.Timeout() || nErr.Temporary()
		} else {
			return false
		}
	}

	buffer := make([]byte, ur.maxBufferSize)
	for {
		select {
		case <-ur.cancelCtx.Done():
			return nil

		default:
			if err := conn.SetReadDeadline(time.Now().Add(ur.readTimeout)); nil != err {
				if !isNetTempErr(err) {
					return err
				} else {
					continue
				}
			}

			if n, _, err := conn.ReadFrom(buffer); nil != err {
				if !isNetTempErr(err) {
					return err
				}
			} else if n > 0 {
				frame := gecko.NewPackFrame(buffer[:n])
				if err := ur.onServeHandler(frame, ctx, deliverer); nil != err {
					return err
				}
			}
		}
	}
	return nil
}

// 改写Union地址的拼接方式
func (ur *UdpInputDevice) GetUnionAddress() string {
	return ur.GetGroupAddress() + ":" + ur.GetPrivateAddress()
}

// 设置Serve处理函数
func (ur *UdpInputDevice) SetServeHandler(handler func([]byte, gecko.Context, gecko.Deliverer) error) {
	ur.onServeHandler = handler
}

func (ur *UdpInputDevice) withTag(f func() *zerolog.Event) *zerolog.Event {
	return f().Str("tag", "UdpInputDevice")
}