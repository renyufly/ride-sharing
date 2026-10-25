package geo

type GeoPoint struct { // 经纬度坐标
	Latitude  float64 // 纬度
	Longitude float64
}

// 提前把某个局部区域转换成二维平面，可以直接计算欧式距离
type Point2D struct { // 平面坐标（米）
	X float64 // meters
	Y float64 // meters
}
