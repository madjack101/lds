package main

import (
	"fmt"
	"net"
	"os"
	"sync"

	"encoding/hex"
	"math/rand"

	"github.com/madjack101/lds/lds"

	"time"

	"github.com/brocaar/loraserver/api/gw"
	"github.com/brocaar/lorawan"

	log "github.com/sirupsen/logrus"
)

type udp struct {
	Server string `toml:"server"`
	Conn   *net.UDPConn
	Mutex  *sync.Mutex
}

func (u *udp) Send(msg []byte) {
	u.Mutex.Lock()
	_, err := u.Conn.Write(msg)

	if err != nil {
		log.Println("udp send failed:", err)
	}

	u.Mutex.Unlock()
}

func (u *udp) init(server string) {
	u.Mutex = &sync.Mutex{}

	addr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		log.Printf("Can't resolve address: ", err)
		os.Exit(1)
	}

	u.Conn, err = net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Println("Can't dial: ", err)
		os.Exit(1)
	}

	go func(u *udp) {
		gweuibytes, _ := hex.DecodeString(config.GW.MAC)
		for {
			data := make([]byte, 1024)
			len, err := u.Conn.Read(data)

			if err != nil {
				log.Println("failed to read UDP msg because of ", err)
				continue
			}

			if len > 0 {
				log.Printf("downlink message: % x", data[:len])
			}

			if len > 12 && data[3] == '\x03' {
				log.Printf("downlink paylad: %v\n", string(data[4:]))
				reply := append(data[0:4], gweuibytes...)
				reply[3] = '\x05'
				u.Send(reply)
				log.Printf("downlink replay: % x\n", reply)
			}

		}
	}(u)

	// send gwstat per 30s
	go func(u *udp) {
		for {
			msg := lds.GwStatPacket()
			log.Printf("send gwstat: % x\n", msg)

			u.Send(msg)

			time.Sleep(time.Duration(30000) * time.Millisecond)
		}

	}(u)

	// send gw keepalived per 3s
	go func(u *udp) {
		for {
			msg := lds.GenGWKeepalived()
			log.Printf("send gwkeep: % x\n", msg)

			u.Send(msg)

			time.Sleep(time.Duration(3000) * time.Millisecond)
		}

	}(u)

}

func RunUdp(config *tomlConfig) {
	lds.InitGWMP()

	if config.UDP.Server != "" {
		config.UDP.init(config.UDP.Server)
	} else {
		os.Exit(-1)
	}

	log.Println("Connection established.")

	//Build your node with known keys (ABP).
	nwkSEncHexKey := config.Device.NwkSEncKey
	sNwkSIntHexKey := config.Device.SNwkSIntKey
	fNwkSIntHexKey := config.Device.FNwkSIntKey
	appSHexKey := config.Device.AppSKey
	devHexAddr := config.Device.Address
	devAddr, err := lds.HexToDevAddress(devHexAddr)
	if err != nil {
		log.Printf("dev addr error: %s", err)
	}

	nwkSEncKey, err := lds.HexToKey(nwkSEncHexKey)
	if err != nil {
		log.Printf("nwkSEncKey error: %s", err)
	}

	sNwkSIntKey, err := lds.HexToKey(sNwkSIntHexKey)
	if err != nil {
		log.Printf("sNwkSIntKey error: %s", err)
	}

	fNwkSIntKey, err := lds.HexToKey(fNwkSIntHexKey)
	if err != nil {
		log.Printf("fNwkSIntKey error: %s", err)
	}

	appSKey, err := lds.HexToKey(appSHexKey)
	if err != nil {
		log.Printf("appskey error: %s", err)
	}

	devEUI, err := lds.HexToEUI(config.Device.EUI)
	if err != nil {
		return
	}

	nwkHexKey := config.Device.NwkKey
	appHexKey := config.Device.AppKey

	appKey, err := lds.HexToKey(appHexKey)
	if err != nil {
		return
	}
	nwkKey, err := lds.HexToKey(nwkHexKey)
	if err != nil {
		return
	}
	appEUI := [8]byte{0, 0, 0, 0, 0, 0, 0, 0}

	device := &lds.Device{
		DevEUI:      devEUI,
		DevAddr:     devAddr,
		NwkSEncKey:  nwkSEncKey,
		SNwkSIntKey: sNwkSIntKey,
		FNwkSIntKey: fNwkSIntKey,
		AppSKey:     appSKey,
		AppKey:      appKey,
		NwkKey:      nwkKey,
		AppEUI:      appEUI,
		UlFcnt:      0,
		DlFcnt:      0,
		Major:       lorawan.Major(config.Device.Major),
		MACVersion:  lorawan.MACVersion(config.Device.MACVersion),
	}

	device.SetMarshaler(config.Device.Marshaler)

	dataRate := &lds.DataRate{
		Bandwidth:    config.DR.Bandwith,
		Modulation:   "LORA",
		SpreadFactor: config.DR.SpreadFactor,
		BitRate:      config.DR.BitRate,
	}

	dataRateStr := fmt.Sprintf("SF%dBW%d", dataRate.SpreadFactor, dataRate.Bandwidth)

	mult := 1

	for {
		if stop {
			stop = false
			return
		}
		payload := []byte{}

		if config.RawPayload.UseRaw {
			var pErr error
			payload, pErr = hex.DecodeString(config.RawPayload.Payload)
			if err != nil {
				log.Errorf("couldn't decode hex payload: %s\n", pErr)
				return
			}
		} else {
			for _, v := range config.DefaultData.Data {
				rand.Seed(time.Now().UnixNano() / 10000)
				if rand.Intn(10) < 5 {
					mult *= -1
				}
				num := float32(v[0])
				if config.DefaultData.Random {
					num = float32(v[0] + float64(mult)*rand.Float64()/100)
				}
				arr := lds.GenerateFloat(num, float32(v[1]), int32(v[2]))
				payload = append(payload, arr...)

			}
		}

		log.Printf("Bytes: % x\n", payload)

		rxInfo := &lds.GwmpRxpk{
			Channel:   config.RXInfo.Channel,
			CodeRate:  config.RXInfo.CodeRate,
			CrcStatus: config.RXInfo.CrcStatus,
			DataRate:  dataRateStr,
			Modu:      dataRate.Modulation,
			Frequency: float32(config.RXInfo.Frequency) / 1000000.0,
			LoRaSNR:   float32(config.RXInfo.LoRaSNR),
			RfChain:   config.RXInfo.RfChain,
			Rssi:      config.RXInfo.RfChain,
			Size:      len(payload),
			Tmst:      uint32(time.Now().UnixNano() / 1000),
		}

		//////

		// gwID, err := lds.MACToGatewayID(config.GW.MAC)
		if err != nil {
			log.Errorf("gw mac error: %s\n", err)
			return
		}
		// now := time.Now()
		// rxTime := ptypes.TimestampNow()
		// tsge := 	ptypes.DurationProto(now.Sub(time.Time{}))

		lmi := &gw.LoRaModulationInfo{
			Bandwidth:       uint32(dataRate.Bandwidth),
			SpreadingFactor: uint32(dataRate.SpreadFactor),
			CodeRate:        rxInfo.CodeRate,
		}

		umi := &gw.UplinkTXInfo_LoraModulationInfo{
			LoraModulationInfo: lmi,
		}

		utx := gw.UplinkTXInfo{
			Frequency:      uint32(rxInfo.Frequency),
			ModulationInfo: umi,
		}

		//////
		mType := lorawan.UnconfirmedDataUp
		if config.Device.MType > 0 {
			mType = lorawan.ConfirmedDataUp
		}

		//Now send an uplink
		msg, err := device.UplinkMessageGWMP(mType, 1, rxInfo, &utx, payload, config.GW.MAC, config.Band.Name, *dataRate)
		if err != nil {
			log.Printf("couldn't generate uplink: %s\n", err)
			break
		}

		log.Printf("Marshaled message: %v\n", string(msg))

		// send by udp
		gwmphead := lds.GenGWMP(config.GW.MAC)

		msg = append(gwmphead[:], msg[:]...)
		log.Printf("msg: % x\n", msg)

		config.UDP.Mutex.Lock()
		_, err = config.UDP.Conn.Write(msg)
		if err != nil {
			log.Println("failed:", err)
			os.Exit(1)
		}
		config.UDP.Mutex.Unlock()

		device.UlFcnt++

		time.Sleep(time.Duration(config.DefaultData.Interval) * time.Millisecond)

	}

}