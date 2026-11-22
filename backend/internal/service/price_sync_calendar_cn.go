package service

import "time"

var tariffBeijing = time.FixedZone("Asia/Shanghai", 8*60*60)

type cnTariffDateRange struct{ first, last int } // inclusive MMDD, in Beijing

// The State Council publishes one schedule per year. These are the official
// days off, not the make-up workdays. The tariff's weekday rule is independent:
// a Saturday or Sunday does not become peak just because it is a working day.
// https://www.gov.cn/zhengce/content/202511/content_7047090.htm
// https://www.gov.cn/zhengce/content/202611/content_7085101.htm
var cnTariffHolidays = map[int][]cnTariffDateRange{
	2026: {{101, 103}, {215, 223}, {404, 406}, {501, 505}, {619, 621}, {925, 927}, {1001, 1007}},
	2027: {{101, 103}, {205, 213}, {403, 405}, {501, 505}, {609, 609}, {915, 915}, {1001, 1007}},
}

// chinaTariffHoliday distinguishes a known ordinary date from a year for
// which no authoritative calendar has been loaded. It performs no I/O.
func chinaTariffHoliday(at time.Time) (holiday bool, known bool) {
	year, month, day := at.In(tariffBeijing).Date()
	ranges, known := cnTariffHolidays[year]
	if !known {
		return false, false
	}
	mmdd := int(month)*100 + day
	for _, span := range ranges {
		if mmdd >= span.first && mmdd <= span.last {
			return true, true
		}
	}
	return false, true
}
