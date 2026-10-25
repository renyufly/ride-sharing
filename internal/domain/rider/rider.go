package rider

import "ride-sharing/internal/domain/geo"

type Rider struct { // 骑手
	UID      uint64       // 骑手唯一 ID
	Location geo.GeoPoint // 真实经纬度
	Point    geo.Point2D  // 匹配算法计算使用的二维坐标
}
