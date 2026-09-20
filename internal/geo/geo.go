// Package geo validates geographic coordinates and projects them into the
// matcher's local, meter-based coordinate system.
package geo

import (
	"errors"
	"fmt"
	"math"

	"ride-sharing/internal/model"
)

// 骑手和订单原本是“经纬度”，但 KD-Tree / 最近邻算法更适合处理二维平面坐标，
// 所以需要把经纬度转换成以“米”为单位的 (X, Y) 坐标

const EarthRadiusMeters = 6_371_008.8  // 地球平均半径

// BoundingBox is an inclusive latitude/longitude rectangle.
// 定义地图范围，表示一个矩形区域
type BoundingBox struct {
	MinLatitude  float64
	MaxLatitude  float64
	MinLongitude float64
	MaxLongitude float64
}

// SanFranciscoBounds contains the existing demo routes and provides enough
// 给 benchmark 划定一个旧金山附近的实验区域
// 后面的投影算法就是针对城市这种小范围区域设计
var SanFranciscoBounds = BoundingBox{
	MinLatitude:  37.70,
	MaxLatitude:  37.82,
	MinLongitude: -122.52,
	MaxLongitude: -122.35,
}

// 检查经纬度是否合法
func ValidatePoint(point model.GeoPoint) error {
	if math.IsNaN(point.Latitude) || math.IsInf(point.Latitude, 0) {
		return errors.New("latitude must be finite")
	}
	if math.IsNaN(point.Longitude) || math.IsInf(point.Longitude, 0) {
		return errors.New("longitude must be finite")
	}
	if point.Latitude < -90 || point.Latitude > 90 {
		return fmt.Errorf("latitude %.6f is outside [-90, 90]", point.Latitude)
	}
	if point.Longitude < -180 || point.Longitude > 180 {
		return fmt.Errorf("longitude %.6f is outside [-180, 180]", point.Longitude)
	}
	return nil
}

func (b BoundingBox) Validate() error {
	if err := ValidatePoint(model.GeoPoint{Latitude: b.MinLatitude, Longitude: b.MinLongitude}); err != nil {
		return fmt.Errorf("invalid minimum corner: %w", err)
	}
	if err := ValidatePoint(model.GeoPoint{Latitude: b.MaxLatitude, Longitude: b.MaxLongitude}); err != nil {
		return fmt.Errorf("invalid maximum corner: %w", err)
	}
	if b.MinLatitude >= b.MaxLatitude {
		return errors.New("minimum latitude must be less than maximum latitude")
	}
	if b.MinLongitude >= b.MaxLongitude {
		return errors.New("minimum longitude must be less than maximum longitude")
	}
	return nil
}

// 求区域中心点：取平均值
func (b BoundingBox) Center() model.GeoPoint {
	return model.GeoPoint{
		Latitude:  (b.MinLatitude + b.MaxLatitude) / 2,
		Longitude: (b.MinLongitude + b.MaxLongitude) / 2,
	}
}

// 判断点是否在区域里面
func (b BoundingBox) Contains(point model.GeoPoint) bool {
	return point.Latitude >= b.MinLatitude && point.Latitude <= b.MaxLatitude &&
		point.Longitude >= b.MinLongitude && point.Longitude <= b.MaxLongitude
}


// Projector uses an equirectangular projection around one fixed origin. It is
// suitable for the city-sized area used by this exercise.
// 经纬度 → 本地 XY 米制坐标转换器
// 用的是一种简单的等距圆柱投影的局部近似
// 把局部地球表面近似成二维平面，是这个 benchmark 为了性能与实现复杂度之间的平衡做出的选择
type Projector struct {
	origin          model.GeoPoint  // 把地图上的哪个经纬度定义成 (0,0)
	originLatitude  float64
	originLongitude float64
	longitudeScale  float64
}

func NewProjector(origin model.GeoPoint) (Projector, error) {
	if err := ValidatePoint(origin); err != nil {
		return Projector{}, fmt.Errorf("invalid projection origin: %w", err)
	}

	// 转换成弧度
	latitudeRadians := degreesToRadians(origin.Latitude)

	return Projector{
		origin:          origin,
		originLatitude:  latitudeRadians,
		originLongitude: degreesToRadians(origin.Longitude),
		longitudeScale:  math.Cos(latitudeRadians), // cos() 只计算一次: 把重复计算提前预计算（precomputation）
	}, nil
}

func (p Projector) Origin() model.GeoPoint {
	return p.origin
}

func (p Projector) Project(point model.GeoPoint) (model.Point2D, error) {
	if err := ValidatePoint(point); err != nil {
		return model.Point2D{}, err
	}

	return model.Point2D{
		X: EarthRadiusMeters * (degreesToRadians(point.Longitude) - p.originLongitude) * p.longitudeScale,
		Y: EarthRadiusMeters * (degreesToRadians(point.Latitude) - p.originLatitude),
	}, nil
}

func degreesToRadians(degrees float64) float64 {
	// radians = degrees × π / 180
	return degrees * math.Pi / 180
}
