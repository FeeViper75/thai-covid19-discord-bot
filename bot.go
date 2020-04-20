package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/dustin/go-humanize"
	"github.com/golang/freetype/truetype"
	"github.com/wcharczuk/go-chart"
	"github.com/wcharczuk/go-chart/drawing"
)

var (
	days = []string{
		"อาทิตย์",
		"จันทร์",
		"อังคาร",
		"พุธ",
		"พฤหัสบดี",
		"ศุกร์",
		"เสาร์",
	}

	months = []string{
		"มกราคม",
		"กุมภาพันธ์",
		"มีนาคม",
		"เมษายน",
		"พฤษภาคม",
		"มิถุนายน",
		"กรกฎาคม",
		"สิงหาคม",
		"กันยายน",
		"ตุลาคม",
		"พฤษจิกายน",
		"ธันวาคม",
	}

	riskLevels = []string{
		"ความเสี่ยงต่ำ",
		"ความเสียงปานกลาง",
		"ความเสียงสูง",
		"ความเสียงสูงมาก",
	}
)

const (
	messageError = "เกิดข้อผิดพลาด กรุณาลองใหม่ภายหลัง"
	helpMsg      = "พิมพ์ \"/covid\" เพื่อดูรายงานปัจจุบัน\nพิมพ์ \"/covid sub\" เพื่อติดตามข่าวอัตโนมัติทุกวันเวลา 12.00 น.\nพิมพ์ \"/covid unsub\" เพื่อยกเลิกการติดตามข่าว\nพิมพ์ \"/covid check\" เพื่อทดสอบแบบประเมิณความเสี่ยง"
)

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	content := strings.ToLower(m.Content)

	if strings.HasPrefix(content, "/covid") {
		prms := strings.Split(content, " ")
		if len(prms) == 1 || prms[1] == "today" {
			msgData := &discordgo.MessageSend{}
			var embed *discordgo.MessageEmbed
			var file *bytes.Buffer
			t := time.Now()
			t = t.In(loc)
			if embedCache, found := ca.Get("embed"); found {
				embed = embedCache.(*discordgo.MessageEmbed)
			}

			if embed == nil {
				data, err := getData()
				if err != nil {
					s.ChannelMessageSend(m.ChannelID, messageError)
					return
				}
				td, err := time.Parse("02/01/2006 15:04", data.UpdateDate)
				if err != nil {
					s.ChannelMessageSend(m.ChannelID, messageError)
					return
				}
				embed, err = buildEmbed(data)
				if err != nil {
					s.ChannelMessageSend(m.ChannelID, messageError)
					return
				}
				if imgCache, ok := ca.Get(fmt.Sprintf("chart-%s", td.Format("Jan2"))); ok {
					embed.Image = imgCache.(*discordgo.MessageEmbedImage)
				}

				if embed.Image == nil {
					file, err = buildChart()
					if err != nil {
						s.ChannelMessageSend(m.ChannelID, messageError)
						return
					}
					chart := &discordgo.File{
						Name:        fmt.Sprintf("covid-chart-%s.png", td.Format("20060102")),
						ContentType: "image/png",
						Reader:      file,
					}
					msgData.Files = append(msgData.Files, chart)
					t = td
				}
			}

			msgData.Embed = embed
			resp, err := s.ChannelMessageSendComplex(m.ChannelID, msgData)
			if err != nil {
				return
			}
			if embed.Image == nil {
				at := resp.Attachments[0]
				embed.Image = &discordgo.MessageEmbedImage{
					URL:      at.URL,
					ProxyURL: at.ProxyURL,
					Height:   at.Height,
					Width:    at.Width,
				}

				ca.Set(fmt.Sprintf("chart-%s", t.Format("Jan2")), embed.Image, 36*time.Hour)
				ca.Set("embed", embed, 30*time.Minute)
				s.ChannelMessageEditEmbed(m.ChannelID, resp.ID, embed)
			}
		}

		if len(prms) > 1 {
			switch prms[1] {
			case "sub", "subscribe":
				_, err := subscribe(m.ChannelID)
				if err != nil {
					s.ChannelMessageSend(m.ChannelID, messageError)
					return
				}
				/*
					if !ok {
						s.ChannelMessageSend(m.ChannelID, "ช่องนี้ได้ติดตามข่าวอยู่แล้ว")
					}*/

				s.ChannelMessageSend(m.ChannelID, "ติดตามข่าวเรียบร้อย")
				break

			case "unsub", "unsubscribe":
				_, err := unsubscribe(m.ChannelID)
				if err != nil {
					s.ChannelMessageSend(m.ChannelID, messageError)
					return
				}
				/*
					if !ok {
						s.ChannelMessageSend(m.ChannelID, "ช่องนี้ไม่ได้ติดตามข่าว")
					}*/

				s.ChannelMessageSend(m.ChannelID, "ยกเลิกการติดตามข่าวเรียบร้อย")
				break
			case "help":
				s.ChannelMessageSend(m.ChannelID, helpMsg)
				break
			case "check":
				if len(m.GuildID) > 0 {
					s.ChannelMessageSend(m.ChannelID, "แบบสอบถามใช้ได้เฉพาะการส่งข้อความหาบอทโดยตรงเท่านั้น")
				} else {
					err := startCheck(m.ChannelID)
					if err != nil {
						s.ChannelMessageSend(m.ChannelID, err.Error())
					}
				}
				break
			default:
				s.ChannelMessageSend(m.ChannelID, helpMsg)
				break
			}

		}
	}
}

func broadcastSubs() error {
	chList, err := getSubs()
	if err != nil {
		return err
	}
	now := time.Now().In(loc)
	var data *covidData
	//delayNotice := true
	for {
		data, err = getData()
		if err != nil {
			return err
		}

		t, err := time.Parse("02/01/2006 15:04", data.UpdateDate)
		if err != nil {
			return err
		}

		if dateEqual(t, now) {
			now = t
			break
		}
		fmt.Printf("broadcast data not update, retrying...\n")
		time.Sleep(5 * time.Minute)

	}

	embed, err := buildEmbed(data)
	if err != nil {
		return err
	}

	file, err := buildChart()
	if err != nil {
		return err
	}

	chart := &discordgo.File{
		Name:        fmt.Sprintf("covid-chart-%s.png", now.Format("20060102")),
		ContentType: "image/png",
		Reader:      file,
	}
	msgData := &discordgo.MessageSend{
		Embed: embed,
	}
	msgData.Files = append(msgData.Files, chart)

	retriedList := make([]string, 0)
	retriedCount := 1

	for _, ch := range *chList {
		shardID := getShardID(ch.DiscordID)
		resp, err := dgs[shardID].ChannelMessageSendComplex(ch.DiscordID, msgData)
		if err != nil {
			retriedList = append(retriedList, ch.DiscordID)
		}

		if embed.Image == nil {
			at := resp.Attachments[0]
			embed.Image = &discordgo.MessageEmbedImage{
				URL:      at.URL,
				ProxyURL: at.ProxyURL,
				Height:   at.Height,
				Width:    at.Width,
			}
			ca.Set(fmt.Sprintf("chart-%s", now.Format("Jan2")), embed.Image, 36*time.Hour)
			ca.Set("embed", embed, 30*time.Minute)
			dgs[shardID].ChannelMessageEditEmbed(ch.DiscordID, resp.ID, embed)
			msgData = &discordgo.MessageSend{
				Embed: embed,
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	for {
		if len(retriedList) > 0 {
			if retriedCount > 3 {
				fmt.Printf("%v channels unsubscribe after 3 retries\n", len(retriedList))
				ubsubscribeBulk(retriedList)
				break
			}
			fmt.Printf("%v channel failed to deliver. retry attempted: %v\n", len(retriedList), retriedCount)
			tmp := make([]string, 0)
			time.Sleep(1 * time.Minute)
			for _, id := range retriedList {
				_, err = dgs[0].ChannelMessageSendEmbed(id, embed)
				if err != nil {
					tmp = append(tmp, id)
				}
				time.Sleep(100 * time.Millisecond)
			}
			retriedList = tmp
			retriedCount++
		} else {
			break
		}
	}
	now = time.Now().In(loc)
	fmt.Printf("finished broadcast at %s\n", now.Format(time.Stamp))
	err = stampBroadcastDate()
	if err != nil {
		fmt.Printf("error stamp broadcast date %s", err.Error())
	}
	return nil
}

func currentDateTH(t time.Time) string {
	d := days[int(t.Weekday())]
	m := months[int(t.Month())-1]

	return fmt.Sprintf("วัน%sที่ %v %s %v", d, t.Day(), m, t.Year()+543)
}

func buildEmbed(data *covidData) (*discordgo.MessageEmbed, error) {
	t, err := time.Parse("02/01/2006 15:04", data.UpdateDate)
	if err != nil {
		return nil, err
	}
	embed := discordgo.MessageEmbed{
		Title: "รายงานสถานการณ์ โควิด-19 ในประเทศไทย",
		/*
			Author: &discordgo.MessageEmbedAuthor{
				Name:    cfg.Author.Name,
				IconURL: cfg.Author.Icon,
				URL:     cfg.Author.URL,
			},*/

		Description: fmt.Sprintf("%s", currentDateTH(t)),
		Color:       16721136,
		Provider: &discordgo.MessageEmbedProvider{
			Name: "กรมควบคุมโรค",
			URL:  "http://covid19.ddc.moph.go.th/",
		},
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🤒 ติดเชื้อสะสม",
				Value:  fmt.Sprintf("%s (เพิ่มขึ้น %s)", humanize.Comma(int64(data.Confirmed)), humanize.Comma(int64(data.NewConfirmed))),
				Inline: true,
			},
			{
				Name:   "💀 เสียชีวิต",
				Value:  fmt.Sprintf("%s (เพิ่มขึ้น %s)", humanize.Comma(int64(data.Deaths)), humanize.Comma(int64(data.NewDeaths))),
				Inline: true,
			},
			{
				Name:   "💪 หายแล้ว",
				Value:  fmt.Sprintf("%s (เพิ่มขึ้น %s)", humanize.Comma(int64(data.Recovered)), humanize.Comma(int64(data.NewRecovered))),
				Inline: true,
			},
			{
				Name:   "🏥 รักษาอยู่ใน รพ.",
				Value:  fmt.Sprintf("%s", humanize.Comma(int64(data.Hospitalized))),
				Inline: true,
			},
			{
				Name:   "🟥 อัตราการเสียชีวิต",
				Value:  fmt.Sprintf("%.2f%%", (float64(data.Deaths)/float64(data.Confirmed))*100),
				Inline: true,
			},
			{
				Name:   "🟩 อัตราการหาย",
				Value:  fmt.Sprintf("%.2f%%", (float64(data.Recovered)/float64(data.Confirmed))*100),
				Inline: true,
			},
		},
		URL: "https://covid19.ddc.moph.go.th/",
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("ข้อมูลโดย กรมควบคุมโรค\nบอทโดย %s\n%s", cfg.Author.Name, cfg.Author.URL),
		},
	}

	return &embed, nil
}

func dateEqual(date1, date2 time.Time) bool {
	y1, m1, d1 := date1.Date()
	y2, m2, d2 := date2.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

func getShardID(channelID string) int {
	if cfg.ShardCount == 1 {
		return 0
	}
	gid, err := strconv.ParseUint(channelID, 10, 64)
	if err != nil {
		return 0
	}
	shardID := (gid >> 22) % uint64(cfg.ShardCount)
	return int(shardID)
}

func buildChart() (*bytes.Buffer, error) {
	data, err := getChartData()
	if err != nil {
		return nil, err
	}

	dlen := len(data.Data) - 30
	if dlen < 0 {
		dlen = 0
	}

	ttfData, err := ioutil.ReadFile("font/Kanit-Medium.ttf")
	if err != nil {
		log.Fatal(err)
	}
	f, err := truetype.Parse(ttfData)
	if err != nil {
		log.Fatal(err)
	}

	dt := data.Data[dlen:]
	dlen = len(dt)
	ticks := make([]chart.Tick, dlen)
	max := 0
	c := chart.ContinuousSeries{
		Name:    "ติดเชื้อสะสม",
		XValues: make([]float64, dlen),
		YValues: make([]float64, dlen),
		Style: chart.Style{
			StrokeColor: drawing.ColorFromHex("e1298e"),
			FillColor:   drawing.ColorFromHex("e1298e").WithAlpha(32),
			Show:        true,
		},
	}
	d := chart.ContinuousSeries{
		Name:    "เสียชีวิต",
		XValues: make([]float64, dlen),
		YValues: make([]float64, dlen),
		Style: chart.Style{
			StrokeColor: drawing.ColorBlack,
			FillColor:   drawing.ColorBlack.WithAlpha(32),
			Show:        true,
		},
	}
	r := chart.ContinuousSeries{
		Name:    "หายแล้ว",
		XValues: make([]float64, dlen),
		YValues: make([]float64, dlen),
		Style: chart.Style{
			StrokeColor: drawing.ColorFromHex("046034"),
			FillColor:   drawing.ColorFromHex("046034").WithAlpha(32),
			Show:        true,
		},
	}
	h := chart.ContinuousSeries{
		Name:    "รักษาอยู่ใน รพ.",
		XValues: make([]float64, dlen),
		YValues: make([]float64, dlen),
		Style: chart.Style{
			StrokeColor: drawing.ColorFromHex("179c9b"),
			FillColor:   drawing.ColorFromHex("179c9b").WithAlpha(32),
			Show:        true,
		},
	}
	for i, v := range dt {
		t, err := time.Parse("01/02/2006", dt[i].Date)
		if err != nil {
			return nil, err
		}
		xv := float64(t.Unix())
		ticks[i] = chart.Tick{Value: xv}
		if (i+1)%5 == 0 || i == 0 {
			ticks[i].Label = fmt.Sprintf("%v %s", t.Day(), months[t.Month()-1])
		}
		c.XValues[i] = xv
		d.XValues[i] = xv
		r.XValues[i] = xv
		h.XValues[i] = xv

		c.YValues[i] = float64(v.Confirmed)
		d.YValues[i] = float64(v.Deaths)
		r.YValues[i] = float64(v.Recovered)
		h.YValues[i] = float64(v.Hospitalized)

		if v.Confirmed > max {
			max = v.Confirmed
		}
	}
	graph := chart.Chart{
		Font:   f,
		Height: 300,
		Width:  600,
		XAxis: chart.XAxis{
			Ticks: ticks,
			Style: chart.StyleShow(),
		},
		YAxis: chart.YAxis{
			Range: &chart.ContinuousRange{
				Min: 0.0,
				Max: float64(max),
			},
			Style: chart.StyleShow(),
			ValueFormatter: func(v interface{}) string {
				if vf, isFloat := v.(float64); isFloat {
					return fmt.Sprintf("%s", humanize.Comma(int64(vf)))
				}
				return ""
			},
		},
		Series: []chart.Series{c, d, r, h},
	}
	graph.Elements = []chart.Renderable{
		chart.Legend(&graph),
	}
	buf := new(bytes.Buffer)

	err = graph.Render(chart.PNG, buf)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	return buf, nil
}

func startCheck(channelID string) error {
	embed := &discordgo.MessageEmbed{
		Title:       "ตรวจระดับความเสี่ยงและคำแนะนำในการปฏิบัติตน COVID19",
		Color:       16721136,
		URL:         "https://covid19.th-stat.com/th/self_screening?ans=",
		Description: "ข้อ 1/8\nผู้ป่วยมีอุณหภูมิกายตั้งแต่ 37.5 องศาขึ้นไป หรือ รู้สึกว่ามีไข้",
	}
	msg, err := dgs[0].ChannelMessageSendEmbed(channelID, embed)
	if err != nil {
		return err
	}
	err = dgs[0].MessageReactionAdd(channelID, msg.ID, "✅")
	if err != nil {
		return err
	}
	err = dgs[0].MessageReactionAdd(channelID, msg.ID, "❌")
	if err != nil {
		return err
	}

	return nil
}

func checkReactionAdd(s *discordgo.Session, m *discordgo.MessageReactionAdd) {
	if m.UserID == s.State.User.ID {
		return
	}
	val := 2
	if m.Emoji.Name == "✅" {
		val = 1
	} else if m.Emoji.Name == "❌" {
		val = 0
	}
	checkUpdateEmbed(s, m.ChannelID, m.MessageID, val)
}

func checkReactionRemove(s *discordgo.Session, m *discordgo.MessageReactionRemove) {
	if m.UserID == s.State.User.ID {
		return
	}
	val := 2
	if m.Emoji.Name == "✅" {
		val = 1
	} else if m.Emoji.Name == "❌" {
		val = 0
	}
	checkUpdateEmbed(s, m.ChannelID, m.MessageID, val)
}

func checkUpdateEmbed(s *discordgo.Session, chID, msgID string, val int) {

	msg, err := s.ChannelMessage(chID, msgID)
	if err != nil {
		s.ChannelMessageSend(chID, err.Error())
	}

	if msg != nil && msg.Embeds != nil && len(msg.Embeds) > 0 && val != 2 {
		embed := msg.Embeds[0]
		if embed.Title == "ตรวจระดับความเสี่ยงและคำแนะนำในการปฏิบัติตน COVID19" {
			u, _ := url.Parse(embed.URL)
			q, _ := url.ParseQuery(u.RawQuery)
			ansq := q.Get("ans")
			ansStr := strings.Split(ansq, ",")
			var al int
			if len(ansq) == 0 {
				al = 0
				embed.URL += fmt.Sprintf("%v", val)
			} else {
				al = len(ansStr)
				embed.URL += fmt.Sprintf(",%v", val)
			}
			switch al {
			case 0:
				embed.Description = "ข้อ 2/8\nผู้ป่วยมีอาการระบบทางเดินหายใจ อย่างใดอย่างหนึ่งดังต่อไปนี้ \"ไอ น้ำมูก เจ็บคอ หายใจเหนื่อย หรือหายใจลำบาก\""
				break
			case 1:
				embed.Description = "ข้อ 3/8\nผู้ป่วยมีประวัติเดินทางไปยัง หรือ มาจาก หรือ อาศัยอยู่ในพื้นที่เกิดโรค COVID-19 ในช่วงเวลา 14 วัน ก่อนป่วย"
				break
			case 2:
				embed.Description = "ข้อ 4/8\nอยู่ใกล้ชิดกับผู้ป่วยยืนยัน COVID-19 (ใกล้กว่า 1 เมตร นานเกิน 5 นาที) ในช่วง 14 วันก่อน"
				break
			case 3:
				embed.Description = "ข้อ 5/8\nมีประวัติไปสถานที่ชุมนุมชน หรือสถานที่ที่มีการรวมกลุ่มคน เช่น ตลาดนัด ห้างสรรพสินค้า สถานพยาบาล หรือ ขนส่งสาธารณะ"
				break
			case 4:
				embed.Description = "ข้อ 6/8\nผู้ป่วยประกอบอาชีพที่สัมผัสใกล้ชิดกับนักท่องเที่ยวต่างชาติ สถานที่แออัด หรือติดต่อคนจำนวนมาก"
				break
			case 5:
				embed.Description = "ข้อ 7/8\nเป็นบุคลากรทางการแพทย์"
				break
			case 6:
				embed.Description = "ข้อ 8/8\nมีผู้ใกล้ชิดป่วยเป็นไข้หวัดพร้อมกัน มากกว่า 5 คน ในช่วงสัปดาห์ที่ป่วย"
				break
			case 7:
				ans := make([]int, len(ansStr))
				for i, v := range ansStr {
					ans[i], err = strconv.Atoi(v)
					if err != nil {
						s.ChannelMessageSend(chID, "เกิดข้อผิดพลาด โปรดทำแบบสอบถามใหม่อีกครั้ง")
						return
					}
				}
				found := false
				for _, v := range checkResults {
					if v.Fever == ans[0] &&
						v.OneURISymp == ans[1] &&
						v.TravelRiskCountry == ans[2] &&
						v.Covid19Contact == ans[3] &&
						(v.CloseRiskCountry == ans[4] || v.CloseRiskLocation == ans[4]) &&
						v.IntContact == ans[5] &&
						v.MedProf == ans[6] &&
						v.CloseCon == val {
						found = true
						embed.Description = "ผลการทดสอบ"
						embed.Fields = []*discordgo.MessageEmbedField{
							{
								Name:  "ระดับความเสี่ยง",
								Value: riskLevels[v.RiskLevel-1],
							},
							{
								Name:  "คำแนะนำเบื้องต้น",
								Value: v.GenAction,
							},
						}
						if len(v.SpecAction) > 0 {
							embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
								Name:  "คำแนะนำแบบเจาะจง",
								Value: v.SpecAction,
							})
						}
						break
					}
				}
				if found {
					_, err = s.ChannelMessageEditEmbed(chID, msg.ID, embed)
					if err != nil {
						s.ChannelMessageSend(chID, "เกิดข้อผิดพลาด โปรดทำแบบสอบถามใหม่อีกครั้ง")
						return
					}
				} else {
					// default answer
					v := searchResult(177)
					found = true
					embed.Description = "ผลการทดสอบ"
					embed.Fields = []*discordgo.MessageEmbedField{
						{
							Name:  "ระดับความเสี่ยง",
							Value: riskLevels[v.RiskLevel-1],
						},
						{
							Name:  "คำแนะนำเบื้องต้น",
							Value: v.GenAction,
						},
					}
					if len(v.SpecAction) > 0 {
						embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
							Name:  "คำแนะนำแบบเจาะจง",
							Value: v.SpecAction,
						})
					}
					_, err = s.ChannelMessageEditEmbed(chID, msg.ID, embed)
					if err != nil {
						s.ChannelMessageSend(chID, "เกิดข้อผิดพลาด โปรดทำแบบสอบถามใหม่อีกครั้ง")
						return
					}
				}
				return
			case 8:
				return
			default:
				return
			}
			_, err = s.ChannelMessageEditEmbed(chID, msg.ID, embed)
			if err != nil {
				s.ChannelMessageSend(chID, "เกิดข้อผิดพลาด โปรดทำแบบสอบถามใหม่อีกครั้ง")
				return
			}
		}
	}
}

func searchResult(idx int) *checkResult {
	for _, v := range checkResults {
		if v.Index == idx {
			return &v
		}
	}
	return nil
}
