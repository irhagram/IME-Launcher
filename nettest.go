package main

import (
	"math"
	"net"
	"strconv"
	"time"
)

const (
	probeRounds  = 30
	probeEvery   = time.Second
	tcpTimeout   = 2 * time.Second
	udpTimeout   = 900 * time.Millisecond
	udpQueryText = "getinfo imelauncher"
)

type NetResult struct {
	Gate   string  `json:"gate"`
	Host   string  `json:"host"`
	OK     bool    `json:"ok"`
	Sent   int     `json:"sent"`
	Recv   int     `json:"recv"` // TCP replies
	Loss   float64 `json:"loss"` // UDP packet loss (what the game uses)
	Min    float64 `json:"min"`
	Avg    float64 `json:"avg"`
	Max    float64 `json:"max"`
	Jitter float64 `json:"jitter"`
	Rating string  `json:"rating"`
	Level  int     `json:"level"` // 0 bad .. 3 excellent
	Error  string  `json:"error,omitempty"`
}

func rate(avg, loss float64, recv int) (string, int) {
	switch {
	case recv == 0:
		return "Tidak Terhubung", 0
	case loss > 15 || avg >= 150:
		return "Buruk", 0
	case loss > 6 || avg >= 80:
		return "Sedang", 1
	case loss > 2 || avg >= 30:
		return "Baik", 2
	}
	return "Sangat Baik", 3
}

// udpProbe sends a FiveM connectionless "getinfo" and returns the round trip in ms (-1 = no reply).
func udpProbe(addr string) float64 {
	c, err := net.Dial("udp", addr)
	if err != nil {
		return -1
	}
	defer c.Close()
	start := time.Now()
	_ = c.SetDeadline(start.Add(udpTimeout))
	if _, err := c.Write(append([]byte{0xff, 0xff, 0xff, 0xff}, udpQueryText...)); err != nil {
		return -1
	}
	buf := make([]byte, 2048)
	if _, err := c.Read(buf); err != nil {
		return -1
	}
	return float64(time.Since(start).Microseconds()) / 1000
}

type stats struct {
	rtts           []float64
	udpSent, udpOK int
	tcpSent, tcpOK int
	min, max       float64
	total, jsum    float64
}

func (s *stats) addTCP(ms float64) {
	s.tcpSent++
	if ms < 0 {
		return
	}
	if s.tcpOK == 0 {
		s.min, s.max = ms, ms
	} else {
		s.jsum += math.Abs(ms - s.rtts[len(s.rtts)-1])
	}
	s.rtts = append(s.rtts, ms)
	s.tcpOK++
	s.total += ms
	s.min, s.max = math.Min(s.min, ms), math.Max(s.max, ms)
}

func (s *stats) avg() float64 {
	if s.tcpOK == 0 {
		return 0
	}
	return s.total / float64(s.tcpOK)
}

func (s *stats) jitter() float64 {
	if s.tcpOK < 2 {
		return 0
	}
	return s.jsum / float64(s.tcpOK-1)
}

// loss is UDP loss (as the game sees it); falls back to TCP failures if UDP never answers.
func (s *stats) loss() float64 {
	if s.udpOK > 0 && s.udpSent > 0 {
		return float64(s.udpSent-s.udpOK) / float64(s.udpSent) * 100
	}
	if s.tcpSent > 0 {
		return float64(s.tcpSent-s.tcpOK) / float64(s.tcpSent) * 100
	}
	return 0
}

// TestGate measures this PC -> gate: ping from TCP connect time (all probes counted)
// and packet loss from UDP queries, one round per second, reporting live progress.
func (a *App) TestGate(gateID string) NetResult {
	if !a.allowed() {
		return NetResult{Gate: gateID, Error: a.lockedMsg()}
	}
	g, ok := a.gate(gateID)
	if !ok {
		return NetResult{Gate: gateID, Error: "gate tidak dikenal"}
	}
	addr := net.JoinHostPort(g.Host, strconv.Itoa(g.Port))
	res := NetResult{Gate: g.ID, Host: g.Host, Sent: probeRounds}
	var s stats

	for i := 0; i < probeRounds; i++ {
		round := time.Now()

		tcpMs := -1.0
		if c, err := net.DialTimeout("tcp", addr, tcpTimeout); err == nil {
			tcpMs = float64(time.Since(round).Microseconds()) / 1000
			c.Close()
		} else if res.Error == "" {
			res.Error = err.Error()
		}
		s.addTCP(tcpMs)

		udpMs := udpProbe(addr)
		s.udpSent++
		if udpMs >= 0 {
			s.udpOK++
		}

		a.emit("net:progress", map[string]any{
			"gate": g.ID, "i": i + 1, "n": probeRounds,
			"ms": tcpMs, "udpLost": udpMs < 0,
			"avg": s.avg(), "min": s.min, "max": s.max, "jitter": s.jitter(), "loss": s.loss(),
			"ok": s.tcpOK,
		})
		if i < probeRounds-1 {
			if rest := probeEvery - time.Since(round); rest > 0 {
				time.Sleep(rest)
			}
		}
	}

	res.Recv = s.tcpOK
	res.Loss = s.loss()
	if s.tcpOK > 0 {
		res.OK, res.Error = true, ""
		res.Min, res.Max, res.Avg, res.Jitter = s.min, s.max, s.avg(), s.jitter()
	}
	res.Rating, res.Level = rate(res.Avg, res.Loss, res.Recv)
	return res
}
