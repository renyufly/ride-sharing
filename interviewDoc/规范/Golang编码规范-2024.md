### 浅谈 golang 代码规范, 性能优化和需要注意的坑

#### 编码规范

##### [强制] 声明slice

声明 slice 最好使用

```plain
var t []int
```

而不是使用 t := make([]int, 0)， 因为 var 并没有初始化，但是 make 初始化了。

但是如果要指定 slice 的长度或者 cap，可以使用 make

##### 最小作用域

```plain
if err := DoSomething(); err != nil {
    return err
}
```

说明：尽量减少作用域, GC 比较友好

##### 赋值规范

声明一个对象有4种方式：make, new(), var, :=

比如:

```plain
t := make([]int, 0)
u := new(User)
var t []int
u := &User{}
```

var 声明但是不立刻初始化

:= 声明并立刻使用

尽量减少使用 new() 因为他不会初始化值, 使用 u := User{} 更好

##### 接口命名

单个功能使用 er 结尾或者名词

```plain
type Reader interface {
    Read(p []byte) (n int, err error)
}
```

2 个功能

```plain
type ReaderWriter interface {
    Reader
    Writer
}
```

3 个及以上功能

```plain
type Car interface {
    Drive()
    Stop()
    Recover()
}
```

#### 命名规范

##### ** [强制] go 文件使用下划线命名 **

##### ** [强制] 常量使用下划线或者驼峰命名, 表达清除不要嫌名字太长 **

##### ** [推荐] 不要在名字中携带类型信息 **

```plain
// 反例
userMap := map[string]User{}
// 正例
users := map[string]User{}
```

##### [推荐] 方法的参数要能表达含义

```plain
// 反例
func CopyFile(a, b string) error
// 正例
func CopyFile(src, dst string) error
```

##### [推荐] 包名一律使用小写字母, 不要加下划线或者中划线

##### [推荐] 如果使用了设计模式, 名称中体现设计模式的含义

```plain
type AppFactory interface {
    CreateApp() App
}
```

##### [推荐] 如果变量名是 bool 类型, 如果字段名不能表达 bool 类型, 可以使用 is 或者 has 前缀

```plain
var isDpdk bool
```

##### [强制] 一个变量只能有一个功能, 并和名称一致, 不要把一个变量作为多种用途

##### embed types优缺点

embed types 指的是我们在 struct 里面定义的匿名的字段，如：

```
type Foo struct {
    Bar
}
type Bar struct {
    Baz int
}
```

那么在上面这个例子中，我们可以通过 `Foo.Baz`直接访问到成员变量，当然也可以通过 `Foo.Bar.Baz`访问。

这样在很多时候可以增加我们使用的便捷性，如果没有使用 embed types 那么可能需要很多代码，如下：

```
type Logger struct {
        writeCloser io.WriteCloser
}

func (l Logger) Write(p []byte) (int, error) {
        return l.writeCloser.Write(p)
}

func (l Logger) Close() error {
        return l.writeCloser.Close()
}

func main() {
        l := Logger{writeCloser: os.Stdout}
        _, _ = l.Write([]byte("foo"))
        _ = l.Close()
}
```

如果使用了 embed types 我们的代码可以变得很简洁：

```
type Logger struct {
        io.WriteCloser
}

func main() {
        l := Logger{WriteCloser: os.Stdout}
        _, _ = l.Write([]byte("foo"))
        _ = l.Close()
}
```

但是同样它也有缺点，有些字段我们并不想 export ，但是 embed types 可能给给我们带出去，例如：

```
type InMem struct {
    sync.Mutex
    m map[string]int
}

func New() *InMem {
     return &InMem{m: make(map[string]int)}
}
```

Mutex 一般并不想 export， 只想在 InMem 自己的函数中使用，如：

```
func (i *InMem) Get(key string) (int, bool) {
    i.Lock()
    v, contains := i.m[key]
    i.Unlock()
    return v, contains
}
```

但是这么写却可以让拿到 InMem 类型的变量都可以使用它里面的 Lock 方法：

```
m := inmem.New()
m.Lock() // ??
```





#### 关于参数

Functional Options Pattern传递参数

这种方法在很多Go开源库都有看到过使用，比如 zap、GRPC等。

它经常用在需要传递和初始化校验参数列表的时候使用，比如我们现在需要初始化一个 HTTP server，里面可能包含了 port、timeout 等等信息，但是参数列表很多，不能直接写在函数上，并且我们要满足灵活配置的要求，毕竟不是每个 server 都需要很多参数。那么我们可以：

- 设置一个不导出的 struct 叫 options，用来存放配置参数；
- 创建一个类型 `type Option func(options *options) error`，用这个类型来作为返回值；

比如我们现在要给 HTTP server 里面设置一个 port 参数，那么我们可以这么声明一个 WithPort 函数，返回 Option 类型的闭包，当这个闭包执行的时候会将 options 的 port 填充进去：

```
type options struct {
        port *int
}

type Option func(options *options) error

func WithPort(port int) Option {
                // 所有的类型校验，赋值，初始化啥的都可以放到这个闭包里面做
        return func(options *options) error {
                if port < 0 {
                        return errors.New("port should be positive")
                }
                options.port = &port
                return nil
        }
}
```

假如我们现在有一个这样的 Option 函数集，除了上面的 port 以外，还可以填充timeout等。然后我们可以利用 NewServer 创建我们的 server：

```
func NewServer(addr string, opts ...Option) (*http.Server, error) {
        var options options
            // 遍历所有的 Option
        for _, opt := range opts {
                    // 执行闭包
                err := opt(&options)
                if err != nil {
                        return nil, err
                }
        }

        // 接下来可以填充我们的业务逻辑，比如这里设置默认的port 等等
        var port int
        if options.port == nil {
                port = defaultHTTPPort
        } else {
                if *options.port == 0 {
                        port = randomPort()
                } else {
                        port = *options.port
                }
        }

        // ...
}
```

初始化 server：

```
server, err := httplib.NewServer("localhost",
                httplib.WithPort(8080),
                httplib.WithTimeout(time.Second))
```

这样写的话就比较灵活，如果只想生成一个简单的 server，我们的代码可以变得很简单：

```
server, err := httplib.NewServer("localhost")
```



#### slice注意点⚠️

<details class="lake-collapse"><summary id="uffbf83f2"><span class="ne-text" style="color: rgb(51, 51, 51)">区分 slice 的 length 和 capacity</span></summary><p id="u4dfa4cfb" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">首先让我们初始化一个带有 length 和 capacity 的 slice ：</span></p><pre data-language="plain" id="w28Iz" class="ne-codeblock language-plain"><code>s := make([]int, 3, 6)</code></pre><p id="u570ba934" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">在make 函数里面，capacity 是可选的参数。上面这段代码我们创建了一个 length 是 3，capacity 是 6 的 slice，那么底层的数据结构是这样的：</span></p><p id="u49ea936e" class="ne-p"><img src="https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376576-76406e62-ee14-426e-a870-422908b27bc2.png" width="1491" alt="" title="" crop="0,0,1,1" id="u5df2f6cc" class="ne-image" style="color: rgb(51, 51, 51); font-size: 16px"></p><p id="u5f50c150" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">slice 的底层实际上指向了一个数组。当然，由于我们的 length 是 3，所以这样设置</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">s[4] = 0</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">会 panic 的。需要使用 append 才能添加新元素。</span></p><pre data-language="plain" id="knV6Y" class="ne-codeblock language-plain"><code>panic: runtime error: index out of range [4] with length 3</code></pre><p id="u0b71db49" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">当 appned 超过 cap 大小的时候，slice 会自动帮我们扩容，在元素数量小于 1024 的时候每次会扩大一倍，当超过了 1024 个元素每次扩大 25%。</span></p><p id="ub4ba9f16" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">有时候我们会使用</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">：</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">操作符从另一个 slice 上面创建一个新切片：</span></p><pre data-language="plain" id="BDyy0" class="ne-codeblock language-plain"><code>s1 := make([]int, 3, 6)
s2 := s1[1:3]</code></pre><p id="u46f46458" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">实际上这两个 slice 还是指向了底层同样的数组，构如下：</span></p><p id="u4ef796e3" class="ne-p"><img src="https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376726-7f8ec7c6-526a-466e-94a9-bfc4710105cd.png" width="1491" alt="" title="" crop="0,0,1,1" id="u70ae3849" class="ne-image" style="color: rgb(51, 51, 51); font-size: 16px"></p><p id="u360bd202" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">由于指向了同一个数组，那么当我们改变第一个槽位的时候，比如</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">s1[1]=2</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">，实际上两个 slice 的数据都会发生改变：</span></p><p id="u9caa82a0" class="ne-p"><img src="https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376699-b539fbad-5009-4b90-8b15-d53376dbf5b0.png" width="1491" alt="" title="" crop="0,0,1,1" id="uff848be6" class="ne-image" style="color: rgb(51, 51, 51); font-size: 16px"></p><p id="u77c68ec0" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">但是当我们使用 append 的时候情况会有所不同：</span></p><pre data-language="plain" id="j32UY" class="ne-codeblock language-plain"><code>s2 = append(s2, 3)
fmt.Println(s1) // [0 2 0]
fmt.Println(s2) // [2 0 3]</code></pre><p id="ucf4cc945" class="ne-p"><img src="https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376720-ce0bd54a-8538-4b8b-9939-0d8b736ff625.png" width="1491" alt="" title="" crop="0,0,1,1" id="uab37348a" class="ne-image" style="color: rgb(51, 51, 51); font-size: 16px">区分 slice 的 length 和 capacity

首先让我们初始化一个带有 length 和 capacity 的 slice ：

```plain
s := make([]int, 3, 6)
```

在make 函数里面，capacity 是可选的参数。上面这段代码我们创建了一个 length 是 3，capacity 是 6 的 slice，那么底层的数据结构是这样的：

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376576-76406e62-ee14-426e-a870-422908b27bc2.png)

slice 的底层实际上指向了一个数组。当然，由于我们的 length 是 3，所以这样设置 `s[4] = 0` 会 panic 的。需要使用 append 才能添加新元素。

```plain
panic: runtime error: index out of range [4] with length 3
```

当 appned 超过 cap 大小的时候，slice 会自动帮我们扩容，在元素数量小于 1024 的时候每次会扩大一倍，当超过了 1024 个元素每次扩大 25%。

有时候我们会使用 `：`操作符从另一个 slice 上面创建一个新切片：

```plain
s1 := make([]int, 3, 6)
s2 := s1[1:3]
```

实际上这两个 slice 还是指向了底层同样的数组，构如下：

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376726-7f8ec7c6-526a-466e-94a9-bfc4710105cd.png)

由于指向了同一个数组，那么当我们改变第一个槽位的时候，比如 `s1[1]=2`，实际上两个 slice 的数据都会发生改变：

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376699-b539fbad-5009-4b90-8b15-d53376dbf5b0.png)

但是当我们使用 append 的时候情况会有所不同：

```plain
s2 = append(s2, 3)

fmt.Println(s1) // [0 2 0]
fmt.Println(s2) // [2 0 3]
```

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376720-ce0bd54a-8538-4b8b-9939-0d8b736ff625.png)

s1 的 len 并没有被改变，所以看到的还是3元素。

还有一件比较有趣的细节是，如果再接着 append s1 那么第四个元素会被覆盖掉：

```
s1 = append(s1, 4)
    fmt.Println(s1) // [0 2 0 4]
    fmt.Println(s2) // [2 0 4]
```

<img src="https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376678-6358b9ac-c8a5-4ab8-8e07-103a96f397f0.png" width="1491" alt="" title="" crop="0,0,1,1" id="uafc44ce6" class="ne-image" style="color: rgb(51, 51, 51); font-size: 16px">我们再继续 append s2 直到 s2 发生扩容，这个时候会发现 s2 实际上和 s1 指向的不是同一个数组了：

```
s2 = append(s2, 5, 6, 7)
fmt.Println(s1) //[0 2 0 4]
fmt.Println(s2) //[2 0 4 5 6 7]
```

<img src="https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956377284-6c1c9e4e-c59b-42d2-9c26-1e87ad65a9a6.png" width="2193" alt="" title="" crop="0,0,1,1" id="uaff1e616" class="ne-image" style="color: rgb(51, 51, 51); font-size: 16px">区分 slice 的 length 和 capacity

首先让我们初始化一个带有 length 和 capacity 的 slice ：

```plain
s := make([]int, 3, 6)
```

在make 函数里面，capacity 是可选的参数。上面这段代码我们创建了一个 length 是 3，capacity 是 6 的 slice，那么底层的数据结构是这样的：

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376576-76406e62-ee14-426e-a870-422908b27bc2.png)

slice 的底层实际上指向了一个数组。当然，由于我们的 length 是 3，所以这样设置 `s[4] = 0` 会 panic 的。需要使用 append 才能添加新元素。

```plain
panic: runtime error: index out of range [4] with length 3
```

当 appned 超过 cap 大小的时候，slice 会自动帮我们扩容，在元素数量小于 1024 的时候每次会扩大一倍，当超过了 1024 个元素每次扩大 25%。

有时候我们会使用 `：`操作符从另一个 slice 上面创建一个新切片：

```plain
s1 := make([]int, 3, 6)
s2 := s1[1:3]
```

实际上这两个 slice 还是指向了底层同样的数组，构如下：

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376726-7f8ec7c6-526a-466e-94a9-bfc4710105cd.png)

由于指向了同一个数组，那么当我们改变第一个槽位的时候，比如 `s1[1]=2`，实际上两个 slice 的数据都会发生改变：

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376699-b539fbad-5009-4b90-8b15-d53376dbf5b0.png)

但是当我们使用 append 的时候情况会有所不同：

```plain
s2 = append(s2, 3)

fmt.Println(s1) // [0 2 0]
fmt.Println(s2) // [2 0 3]
```

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376720-ce0bd54a-8538-4b8b-9939-0d8b736ff625.png)

s1 的 len 并没有被改变，所以看到的还是3元素。

还有一件比较有趣的细节是，如果再接着 append s1 那么第四个元素会被覆盖掉：

```plain
s1 = append(s1, 4)
    fmt.Println(s1) // [0 2 0 4]
    fmt.Println(s2) // [2 0 4]
```

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376678-6358b9ac-c8a5-4ab8-8e07-103a96f397f0.png)

我们再继续 append s2 直到 s2 发生扩容，这个时候会发现 s2 实际上和 s1 指向的不是同一个数组了：

```plain
s2 = append(s2, 5, 6, 7)
fmt.Println(s1) //[0 2 0 4]
fmt.Println(s2) //[2 0 4 5 6 7]
```

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956377284-6c1c9e4e-c59b-42d2-9c26-1e87ad65a9a6.png)

除了上面这种情况，还有一种情况 append 会产生意想不到的效果：

```
s1 := []int{1, 2, 3}
s2 := s1[1:2]
s3 := append(s2, 10)
```

<img src="https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956377253-94941e02-e967-43d1-84de-e83aa064a430.png" width="1428" alt="" title="" crop="0,0,1,1" id="u35827d76" class="ne-image" style="color: rgb(51, 51, 51); font-size: 16px">区分 slice 的 length 和 capacity

首先让我们初始化一个带有 length 和 capacity 的 slice ：

```plain
s := make([]int, 3, 6)
```

在make 函数里面，capacity 是可选的参数。上面这段代码我们创建了一个 length 是 3，capacity 是 6 的 slice，那么底层的数据结构是这样的：

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376576-76406e62-ee14-426e-a870-422908b27bc2.png)

slice 的底层实际上指向了一个数组。当然，由于我们的 length 是 3，所以这样设置 `s[4] = 0` 会 panic 的。需要使用 append 才能添加新元素。

```plain
panic: runtime error: index out of range [4] with length 3
```

当 appned 超过 cap 大小的时候，slice 会自动帮我们扩容，在元素数量小于 1024 的时候每次会扩大一倍，当超过了 1024 个元素每次扩大 25%。

有时候我们会使用 `：`操作符从另一个 slice 上面创建一个新切片：

```plain
s1 := make([]int, 3, 6)
s2 := s1[1:3]
```

实际上这两个 slice 还是指向了底层同样的数组，构如下：

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376726-7f8ec7c6-526a-466e-94a9-bfc4710105cd.png)

由于指向了同一个数组，那么当我们改变第一个槽位的时候，比如 `s1[1]=2`，实际上两个 slice 的数据都会发生改变：

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376699-b539fbad-5009-4b90-8b15-d53376dbf5b0.png)

但是当我们使用 append 的时候情况会有所不同：

```plain
s2 = append(s2, 3)

fmt.Println(s1) // [0 2 0]
fmt.Println(s2) // [2 0 3]
```

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376720-ce0bd54a-8538-4b8b-9939-0d8b736ff625.png)

s1 的 len 并没有被改变，所以看到的还是3元素。

还有一件比较有趣的细节是，如果再接着 append s1 那么第四个元素会被覆盖掉：

```plain
s1 = append(s1, 4)
    fmt.Println(s1) // [0 2 0 4]
    fmt.Println(s2) // [2 0 4]
```

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956376678-6358b9ac-c8a5-4ab8-8e07-103a96f397f0.png)

我们再继续 append s2 直到 s2 发生扩容，这个时候会发现 s2 实际上和 s1 指向的不是同一个数组了：

```plain
s2 = append(s2, 5, 6, 7)
fmt.Println(s1) //[0 2 0 4]
fmt.Println(s2) //[2 0 4 5 6 7]
```

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956377284-6c1c9e4e-c59b-42d2-9c26-1e87ad65a9a6.png)

除了上面这种情况，还有一种情况 append 会产生意想不到的效果：

```go
s1 := []int{1, 2, 3}
s2 := s1[1:2]
s3 := append(s2, 10)
```

![img](https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956377253-94941e02-e967-43d1-84de-e83aa064a430.png)

如果print 它们应该是这样：

```
s1=[1 2 10], s2=[2], s3=[2 10]
```



        s = []string(nil)
        log(2, s)
    
        s = []string{}
        log(3, s)
    
        s = make([]string, 0)
        log(4, s)

}

func log(i int, s []string) {
fmt.Printf(&quot;%d: empty=%t\tnil=%t\n&quot;, i, len(s) == 0, s == nil)
}</code></pre><p id="uf301c4d3" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">输出：</span></p><pre data-language="plain" id="MMHxi" class="ne-codeblock language-plain"><code>1: empty=true nil=true
2: empty=true nil=true
3: empty=true nil=false
4: empty=true nil=false</code></pre><p id="u53a09a94" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">前两种方式会创建一个 nil 的 slice，后两种会进行初始化，并且这些 slice 的大小都为 0 。</span></p><p id="u3c342262" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">对于</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">var s []string</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">这种方式来说，好处就是不用做任何的内存分配。比如下面场景可能可以节省一次内存分配：</span></p><pre data-language="go" id="jxDyY" class="ne-codeblock language-go"><code>func f() []string {
var s []string
if foo() {
s = append(s, &quot;foo&quot;)
}
if bar() {
s = append(s, &quot;bar&quot;)
}
return s
}</code></pre><p id="u6920c764" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">对于</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">s := []string{}</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">这种方式来说，它比较适合初始化一个已知元素的 slice：</span></p><pre data-language="plain" id="qoDpQ" class="ne-codeblock language-plain"><code>s := []string{&quot;foo&quot;, &quot;bar&quot;, &quot;baz&quot;}</code></pre><p id="uad194feb" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">如果没有这个需求其实用</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">var s []string</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">比较好，反正在使用的适合都是通过 append 添加元素，</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">var s []string</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">还能节省一次内存分配。</span></p><p id="u0ba1dd0c" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">如果我们初始化了一个空的 slice， 那么最好是使用</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">len(xxx) == 0</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">来判断 slice 是不是空的，如果使用 nil 来判断可能会永远非空的情况，因为对于</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">s := []string{}</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">和</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">s = make([]string, 0)</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">这两种初始化都是非nil的。</span></p><p id="u68866ab7" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">对于</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">[]string(nil)</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">这种初始化的方式，使用场景很少，一种比较方便的使用场景是用它来进行 slice 的 copy：</span></p><pre data-language="go" id="mHTsR" class="ne-codeblock language-go"><code>src := []int{0, 1, 2}
dst := append([]int(nil), src...)</code></pre><p id="u05e5a961" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">对于 make 来说，它可以初始化 slice 的 length 和 capacity，如果我们能确定 slice 里面会存放多少元素，从性能的角度考虑最好使用 make 初始化好，因为对于一个空的 slice append 元素进去每次达到阈值都需要进行扩容，下面是填充 100 万元素的 benchmark：</span></p><pre data-language="plain" id="gJTm0" class="ne-codeblock language-plain"><code>BenchmarkConvert_EmptySlice-4 22 49739882 ns/op
BenchmarkConvert_GivenCapacity-4 86 13438544 ns/op
BenchmarkConvert_GivenLength-4 91 12800411 ns/op</code></pre><p id="uc20afa9f" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">可以看到，如果我们提前填充好 slice 的容量大小，性能是空 slice 的四倍，因为少了扩容时元素复制以及重新申请新数组的开销。</span></p><h3 id="copy-slice"><span class="ne-text" style="color: rgb(51, 51, 51)">copy slice</span></h3><pre data-language="plain" id="G9tIT" class="ne-codeblock language-plain"><code>src := []int{0, 1, 2}
var dst []int
copy(dst, src)
fmt.Println(dst) // []</code></pre><p id="u99445e1b" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">使用 copy 函数 copy slice 的时候需要注意，上面这种情况实际上会 copy 失败，因为对 slice 来说是由 length 来控制可用数据，copy 并没有复制这个字段，要想 copy 我们可以这么做：</span></p><pre data-language="plain" id="gCf2G" class="ne-codeblock language-plain"><code>src := []int{0, 1, 2}
dst := make([]int, len(src))
copy(dst, src)
fmt.Println(dst) //[0 1 2]</code></pre><p id="u00b15fdf" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">除此之外也可以用上面提到的：</span></p><pre data-language="plain" id="IfkVC" class="ne-codeblock language-plain"><code>src := []int{0, 1, 2}
dst := append([]int(nil), src...)</code></pre><h3 id="e1a691f7"><span class="ne-text" style="color: rgb(51, 51, 51)">slice capacity内存释放问题</span></h3><p id="udd381993" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">先来看个例子：</span></p><pre data-language="go" id="shVaq" class="ne-codeblock language-go"><code>type Foo struct {
v []byte
}

func keepFirstTwoElementsOnly(foos []Foo) []Foo {
return foos[:2]
}

func main() {
foos := make([]Foo, 1_000)
printAlloc()

    for i := 0; i &lt; len(foos); i++ {
        foos[i] = Foo{
            v: make([]byte, 1024*1024),
        }
    }
    printAlloc()
    
    two := keepFirstTwoElementsOnly(foos)
    runtime.GC()
    printAlloc()
    runtime.KeepAlive(two)

}</code></pre><p id="u6d079c93" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">上面这个例子中使用 printAlloc 函数来打印内存占用：</span></p><pre data-language="go" id="sy96w" class="ne-codeblock language-go"><code>func printAlloc() {
var m runtime.MemStats
runtime.ReadMemStats(&amp;m)
fmt.Printf(&quot;%d KB\n&quot;, m.Alloc/1024)
}</code></pre><p id="uf981e5b2" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">上面 foos 初始化了 1000 个容量的 slice ，里面 Foo struct 每个都持有 1M 内存的 slice，然后通过 keepFirstTwoElementsOnly 返回持有前两个元素的 Foo 切片，我们的想法是手动执行 GC 之后其他的 998 个 Foo 会被 GC 销毁，但是输出结果如下：</span></p><pre data-language="plain" id="jO9Kf" class="ne-codeblock language-plain"><code>387 KB
1024315 KB
1024319 KB</code></pre><p id="ue09efe5d" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">实际上并没有，原因就是实际上 keepFirstTwoElementsOnly 返回的 slice 底层持有的数组是和 foos 持有的同一个：</span></p><p id="u2de8bc3e" class="ne-p"><img src="https://cdn.nlark.com/yuque/0/2024/png/40391975/1734956377365-c6d5662c-120d-4c72-b296-fa2e25f9e015.png" width="1491" alt="" title="" crop="0,0,1,1" id="ud0c23f70" class="ne-image" style="color: rgb(51, 51, 51); font-size: 16px"></p><p id="u276f1afc" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">所以我们真的要只返回 slice 的前2个元素的话应该这样做：</span></p><pre data-language="go" id="rcGxG" class="ne-codeblock language-go"><code>func keepFirstTwoElementsOnly(foos []Foo) []Foo {
res := make([]Foo, 2)
copy(res, foos)
return res
}</code></pre><p id="u749cf948" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">不过上面这种方法会初始化一个新的 slice，然后将两个元素 copy 过去。不想进行多余的分配可以这么做：</span></p><pre data-language="go" id="JG9In" class="ne-codeblock language-go"><code>func keepFirstTwoElementsOnly(foos []Foo) []Foo {
for i := 2; i &lt; len(foos); i++ {
foos[i].v = nil
}
return foos[:2]
}</code></pre><p id="u927cd47c" class="ne-p"><span class="ne-text"><br /></span></p><p id="u8871f826" class="ne-p"><br></p></details>

#### range注意点⚠️

<details class="lake-collapse"><summary id="uff6ead99"><span class="ne-text" style="color: rgb(51, 51, 51)">copy 的问题</span></summary><p id="u1d6db04a" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">使用 range 的时候如果我们直接修改它返回的数据会不生效，因为返回的数据并不是原始数据：</span></p><pre data-language="go" id="Az4im" class="ne-codeblock language-go"><code>type account struct {
    balance float32
}

    accounts := []account{
        {balance: 100.},
        {balance: 200.},
        {balance: 300.},
    }
    for _, a := range accounts {
        a.balance += 1000
    }</code></pre><p id="u580fa45d" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">如果像上面这么做，那么输出的 accounts是：</span></p><pre data-language="plain" id="DHkBU" class="ne-codeblock language-plain"><code>[{100} {200} {300}]</code></pre><p id="u09ab2bcd" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">所以我们想要改变 range 中的数据可以这么做：</span></p><pre data-language="go" id="ThisS" class="ne-codeblock language-go"><code>for i := range accounts {
    accounts[i].balance += 1000

}</code></pre><p id="u09e9f1ed" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">range slice 的话也会copy一份：</span></p><pre data-language="go" id="Oxdch" class="ne-codeblock language-go"><code>s := []int{0, 1, 2}
for range s {
s = append(s, 10)
}</code></pre><p id="u618dbe2a" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">这份代码在 range 的时候会copy一份，因此只会调用三次 append 后停止。</span></p><p id="u1afebf18" class="ne-p"><br></p></details>

#### 变量

<details class="lake-collapse"><summary id="u3fc63485"><span class="ne-text" style="color: rgb(51, 51, 51)">注意 shadow 变量</span></summary><pre data-language="go" id="iI2qP" class="ne-codeblock language-go"><code>var client *http.Client
    if tracing {
        client, err := createClientWithTracing()
        if err != nil {
            return err
        }
        log.Println(client)
    } else {
        client, err := createDefaultClient()
        if err != nil {
            return err
        }
        log.Println(client)
    }</code></pre><p id="ue625a8b7" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">在上面这段代码中，声明了一个 client 变量，然后使用 tracing 控制变量的初始化，可能是因为没有声明 err 的缘故，使用的是</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">:=</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">进行初始化，那么会导致外层的 client 变量永远是 nil。这个例子实际上是很容易发生在我们实际的开发中，尤其需要注意。</span></p><p id="u62065e41" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">如果是因为 err 没有初始化的缘故，我们在初始化的时候可以这么做：</span></p><pre data-language="go" id="G7mg7" class="ne-codeblock language-go"><code>var client *http.Client
    var err error
    if tracing {
        client, err = createClientWithTracing() 
    } else {
        ...
    }
    if err != nil { // 防止重复代码
        return err
    }</code></pre><p id="ued403da2" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">或者干脆内层的变量声明换一个变量名字，这样就不容易出错了</span></p><p id="uc57a05ad" class="ne-p"><br></p></details>
#### 关于init函数
<details class="lake-collapse"><summary id="u4698d99c"><span class="ne-text" style="color: rgb(51, 51, 51)">慎用 init 函数</span></summary><p id="u8a4ac96e" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">使用 init 函数之前需要注意下面几件事：</span></p><h3 id="31eaf9f5"><span class="ne-text" style="color: rgb(51, 51, 51)">init 函数会在全局变量之后被执行</span></h3><p id="u5c5fad7a" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">init 函数并不是最先被执行的，如果声明了 const 或 全局变量，那么init 函数会在它们之后执行：</span></p><pre data-language="go" id="gy9nG" class="ne-codeblock language-go"><code>package main

import &quot;fmt&quot;

var a = func() int {
fmt.Println(&quot;a&quot;)
return 0
}()

func init() {
fmt.Println(&quot;init&quot;)
}

func main() {
fmt.Println(&quot;main&quot;)
}

// output
a
init
main</code></pre><h3 id="2646014d"><span class="ne-text" style="color: rgb(51, 51, 51)">init初始化按解析的依赖关系顺序执行</span></h3><p id="ua3310422" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">比如 main 包里面有 init 函数，依赖了 redis 包，main函数执行了 redis 包的 Store 函数，恰好 redis 包里面也有 init 函数，那么执行顺序会是：</span></p><p id="u9efc7865" class="ne-p"><img src="https://cdn.nlark.com/yuque/0/2024/png/40391975/1734955346585-fa724136-de50-476b-9211-378911f5c33c.png" width="1788" alt="" title="" crop="0,0,1,1" id="uc54f780e" class="ne-image" style="color: rgb(51, 51, 51); font-size: 16px"></p><p id="u026279ee" class="ne-p"><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">还有一种情况，如果是使用</span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><code class="ne-code"><span class="ne-text" style="color: rgb(199, 37, 78); background-color: rgb(249, 242, 244); font-size: 14px">&quot;import \_ foo&quot;</span></code><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px"> </span><span class="ne-text" style="color: rgb(51, 51, 51); font-size: 16px">这种方式引入的，也是会先调用 foo 包中的 init 函数。</span></p><p id="ucb6e8093" class="ne-p"><span class="ne-text"><br /></span></p><p id="u876c1e5d" class="ne-p"><br></p></details>

#### golang 基本规范

##### 包设计

###### [强制] 包的设计满足单一职责

** 说明: 在 SRP (Single Response Principle) 模式中, 单一职责原则是指一个类只负责一项功能, 并且不能负责多个功能. 将包设计的非常内聚, 减少包之间的 api **

###### [强制] 包的设计遵循最小可见性原则

** 说明: 仅在包内调用的函数, 或者变量, 或者结构体, 或者接口, 或者类型, 或者函数等等, 需要小写开头, 不可以可以被外部包访问 **

###### [强制] 代码需要可测试性, 使用接口和依赖注入替代硬编码

###### [强制] 单元测试文件放到代码文件同级目录, 便于 golang 工具使用

比如: vscode 在方法上右键可以直接生成测试代码和测试覆盖率并可视化展示执行情况

##### 布局

###### [推荐] 程序实体之间使用空行区分, 增加可读性

说明: 比如函数中各个模块功能使用空行区分, 增加可读性

###### [推荐] 每个文件末尾应该有且仅有一个空行

###### [推荐] 一元操作符不要加空格, 二元操作符的才需要

##### 注释

###### [推荐] 可导出的方法, 变量, 结构体等都需要注释

##### 表达式和语句

###### [推荐] if 或者循环的嵌套层数不宜大于 3

###### [推荐] 对于 for 遍历, 优先使用 range 而不是显式'的下标, 如果 value 占用内存大的话可以使用显式下标

说明: range 可以让代码更加整洁, 特别是多层 for 嵌套的时候, 但是 range 非拷贝值, 如果 value 不是指针类型, 而且占用内存较大会有性能损耗.

##### 函数

###### [强制] 命名不要暴露实现细节, 一般以"做什么"来命名而不是"怎么做"

###### [推荐] 短小精悍, 尽量控制到 20 行左右

说明: 函数的粒度越小, 可复用的概率越大, 而且函数越多, 高层函数调用起来就是代码可读性很高, 读起来就像一系列解释

###### [推荐] 单一职责, 函数只做好一件事情, 只做一件事情

###### [强制] 不要设置多功能函数

例如: 一个函数既修改了状态, 又返回了状态, 应该拆分

###### [推荐] 为简单的功能编写函数

说明: 为 1,2 行代码编写函数也是必要的, 增加代码的可复用性, 增加高层函数的可读性, 可维护性, 可测试性

##### 参数

###### [推荐] 参数个数限制在 3 个以内, 如果超过了, 可以使用配置类或者 Options 设计模式

说明: 函数的最理想的参数的个数首先是0, 然后是 1, 然后是 2, 3 就会很差了. 因为参数有很多概念性, 可读性差, 而且让测试十分复杂

###### [推荐] 函数不能含有表示参数

说明: 标识参数丑陋不堪, 函数往往根据标识参数走不同的逻辑, 这个和单一职责违背

###### [强制] struct 作为参数传递的时候, 使用指针

说明: 函数的执行就是压栈, struct 如果有多个字段将会被多次压栈, 有性能损失, 指针只会被压栈一次

###### [推荐] 在 api(controller) 层对传入的参数进行检查, 而不是每一层都检查一次

###### [推荐] 当 chan 作为函数的参数的时候, 根据最小权限原则, 使用单向 chan

```plain
// 读取单向chan
func Parse(ch <-chan struct{}) {
 for v := range ch {
  println(v)
 }
}

// 写入单向chan
func Do(down chan<- struct{}) {
 time.Sleep(time.Second)
 down <- struct{}{}
}
```

##### 返回值

###### [推荐] 返回值的个数不要大于 3

###### [强制] 统一定义错误, 不要随便抛出错误

说明: 比如记录不存在可能有多种错误

```plain
"record not exits"
"record not exited"
"record not exited!!"
```

上层函数要处理底层的错误的话, 要知道所有的抛出情况, 这个是不现实的, 需要处理的错误应该使用统一文件定义错误码

###### [强制] 没有失败原因的时候, 不要使用 error

```plain
// 正例
func IsPhone() bool
// 反例
func IsPhone() error
```

###### [推荐] 当多重试几次可以避免失败的时候, 不要返回 error

错误是偶然发生的, 应该给一个机会重试, 可以避免大多数的偶然问题

###### [推荐] 上层函数不关心 error 的时候, 不要返回 error

比如 Close(), Clear() 抛出了 error, 上层函数大概率不知道怎么处理

##### 异常设计

###### [推荐] 程序的开发阶段, 坚持速错, 让异常程序崩溃

说明: 速错的本质逻辑就是 "让它挂", 只有挂了你才第一时间知道错误, panic 能让 bug 尽快被修复

###### [强制] 程序部署后, 应该避免终止

###### 是否 recover 应该根据配置文件确定, 默认需要 recover

###### 注意: 有时候需要在延迟函数中释放资源, 比如 panic 之前 read 了 channel, 但是还没有 write 就 panic , 需要在 deffer 函数中做好处理, 防止 channel 阻塞.

###### [推荐] 当入参不合法的时候, panic

说明: 当入参不合法的时候, panic, 可以让上层函数知道错误, 而不是继续执行(api 应该提前做好参数检查)

##### 整洁测试

###### [强制] 不要为了测试对代码进行入侵式的修改, 应该 mock

###### 说明: 禁止为了测试在函数中增加条件分支和测试变量

###### [推荐] 测试的三要数, 可读性, 可读性, 可读性

###### 生产代码的可靠性由测试代码来保证, 测试代码的可靠性由最简单的可读性来保证, 逻辑需要简单到没有 bug

##### REFERENCE

###### uber go-guide [https://github.com/xxjwxc/uber_go_guide_cn](https://link.zhihu.com/?target=https%3A//github.com/xxjwxc/uber_go_guide_cn)

#### golang 性能优化

##### 内存优化

###### 小对象合并

小对象在堆内存上频繁的创建和销毁, 会导致内存碎片, 一般会选择使用内存池。golang 的内存机制也是内存池, 每个 span 大小为 4KB, 同时维护一个 cache, cache 有一个 list 数组数组里面储存的是链表, 就像 HashMap 的拉链法, 数组的每个格子代表的内存大小是不一样的, 64 位的机器是 8 byte 为基础, 比如下标 0 是 8 byte 大小的链表节点, 下标 1 是 16 byte 的链表节点, 每个下标的内存不一样, 使用的是按需分配最近的内存, 比如一个结构体的内存实际上算下来是 31 byte, 分配的时候会分配 32 byte.一个下标的一条链表的每个 Node 储存的内存是一致的.所以建议将小对象合并为一个 struct。

```plain
for k, v := range m {
    x := struct {k , v string} {k, v} // copy for capturing by the goroutine
    go func() {
        // using x.k & x.v
    }()
}
```

###### 使用 buf 缓存

协议编码的时候需要频繁的操作 buf, 可以使用 bytes.Buffer 作为缓存区对象, 它会一次性分配足够大的内存, 避免内存不够的时候动态申请内存, 减少内存分配次数, 而且, buf 可以被复用(建议复用)

###### slice 和 map 创建的时候, 预估大小指定的容量

预先分配内存, 可以减少动态扩容带来的开销

```plain
t := make([]int, 0, 100)
m := make(map[string]int, 100)
```

如果不确定 slice 会不会初始化, 使用 var 这样不会分配内存, make([]int,0) 会分配内存空间

```plain
var t []int
```

###### 使用指针来优化性能

减少数据拷贝的开销

#### 并发优化

###### goroutine 池化

go 虽然轻量, 但是对于高并发的轻量级任务, 比如高并发的 job 类型的代码, 可以考虑使用 goroutine 池化, 减少 goroutine 的创建和销毁, 减少 goroutine 的创建和销毁的开销

###### 减少系统调用

** goroutine 的实现是通过同步模拟异步操作, 比如下面的操作并不会阻塞： **

runtime 的线程调度

网络IO

channel

time.Sleep

基于底层异步的 SysCall

** 下面的阻塞会创建新的线程调度： **

本地 IO

基于底层同步的 SysCall

CGO 调用 IO 或者其他阻塞

建议将同步调用: 隔离到可控 goroutine 中, 而不是直接高并 goroutine 调用

###### 减少锁, 减少大锁

Go 推荐使用 channel 的方式调用而不是共享内存, channel 之间存在大锁, 可以将锁的力度降低

###### 拓展: channel

channel 不要传递大数据, 会有值拷贝

channel 的底层是唤醒缓冲区+读写等待队列

不要用 channel 传递图片等数据, 任何的队列的性能都很低, 可以尝试指针优化大对象

###### 合并请求 singleflight

** 参考: **[singleflight](https://link.zhihu.com/?target=https%3A//www.lixueduan.com/post/go/singleflight/)

###### 协议压缩 protobuf

protobuf 比 json 的储存效率和解析效率更高, 推荐在持久化或者数据传输的时候使用 protobuf 替代 json

批量协议

对数据访问接口提供批量协议, 比如门面设计模式或者 pipeline, 可以减少非常多的 IO, QPS, 和拆包解包的开销

###### 并行请求 errgroup

对于网关接口, 通常需要聚合多个模块的数据, 当这些业务模块数据之间没有依赖的时候, 可以并行请求, 减少耗时

```plain
ctxTimeout, cf := context.WithTimeout(context.Background(), time.Second)
defer cf()
g, ctx := errgroup.WithContext(ctxTimeout)
var urls = []string{
    "http://www.golang.org/",
    "http://www.google.com/",
    "http://www.somestupidname.com/",
}
for _, url := range urls {
    // Launch a goroutine to fetch the URL.
    url := url // https://golang.org/doc/faq#closures_and_goroutines
    g.Go(func() error {
        // Fetch the URL.
        resp, err := http.Get(url)
        if err == nil {
            resp.Body.Close()
        }
        return err
    })
}
// Wait for all HTTP fetches to complete.
if err := g.Wait(); err == nil {
    fmt.Println("Successfully fetched all URLs.")
}
select {
case <-ctx.Done():
    fmt.Println("Context canceled")
default:
    fmt.Println("Context not canceled")
}
```

#### 需要注意的坑

##### channel 之坑

###### 如何优雅的关闭 channel

参考: [如何优雅的关闭 channel](https://link.zhihu.com/?target=https%3A//www.jianshu.com/p/d24dfbb33781)

###### 关闭 channel 的坑

关闭已经关闭的 channel 会导致 panic

给关闭的 channel 发送数据会导致 panic

从关闭的 channel 中读取数据是初始值默认值

###### channel在用之前一定要初始化

向没有初始化的channel中读或者写，会让当前goroutine陷入死锁，导致协程泄露，一直阻塞，不能被唤醒

###### CCP: Channel Close Principle (关闭通道原则)

不要从接收端关闭 channel

不要关闭有多个发送端的 channel

当发送端只有一个且后面不会再发送数据才可以关闭 channel

###### 有缓存的 channel 不一定有序

##### defer 之坑

###### 参数传递是在调用的时候

```plain
i := 1
defer println("defer", i)
i++
// defer 1
```

###### 非参数的闭包

```plain
i := 1
defer func() {
    println("defer", i)
}()
i++
// defer 2
```

###### 有名返回同理闭包, 并且会修改有名返回的返回值

```plain
func main(){
 fmt.Printf("main: %v\n", getNum())
 // defer 2
 // main: 2
}

func getNum() (i int) {
 defer func() {
  i++
  println("defer", i)
 }()
 i++
 return
}
```

###### 不要 for 循环中调用 deffer

因为 deffer 只会在函数 return 之后执行, 这样会累积大量的 deffer 而且极其容易出错

** 建议: 将 for 循环需要 deffer 的代码逻辑封装为一个函数 **

##### HTTP 之坑

###### request 超时时间

golang 的 http 默认的 request 没有超时, 这是一个大坑, 因为如果服务器没有响应, 也没有断开, 客户端会一直等待, 导致客户端阻塞, 量一上来就崩溃了

###### 关闭 HTTP 的 response

http 请求框架的 response 一定要通过 Close 方法关闭, 不然有可能内存泄露

##### interface 之坑

###### interface 到底什么才等于 nil?

说明: interface{}和接口类型 不同于 struct, 接口底层有 2 个成员, 一个是 type 一个是 value, 只有当 type 和 value 都为 nil 时, interface{} 才等于 nil

```plain
var u interface{} = (*interface{})(nil)
if u == nil {
    t.Log("u is nil")
} else {
    t.Log("u is not nil")
}
// u is not nil
接口
var u Car = (Car)(nil)
if u == nil {
    t.Log("u is nil")
} else {
    t.Log("u is not nil")
}
// u is nil
```

自定义的 struct

```plain
var u *user = (*user)(nil)
if u == nil {
    t.Log("u is nil")
} else {
    t.Log("u is not nil")
}
// u is nil
```

##### map 之坑

###### map 并发读写会 panic, 需要加锁或者使用 sync.Map

###### map 不能直接更新 value 的某一个字段

```plain
type User struct{
 name string
}
func TestMap(t *testing.T) {
 m := make(map[string]User)
 m["1"] = User{name:"1"}
 m["1"].name = "2"
 // 编译失败，不能直接修改map的一个字段值
}
```

需要单独拿出来

```plain
func TestMap(t *testing.T) {
 m := make(map[string]User)
 m["1"] = User{name: "1"}
 u1 := m["1"]
 u1.name = "2"
}
```

##### 切片之坑

###### 数组是值类型, 切片是引用类型(指针)

```plain
func TestArray(t *testing.T) {
 a := [1]int{}
 setArray(a)
 println(a[0])
 // 0
}
func setArray(a [1]int) {
 a[0] = 1
}
func TestSlice(t *testing.T) {
 a := []int{
  1,
 }
 setSlice(a)
 println(a[0])
 // 1
}
func setSlice(a []int) {
 a[0] = 1
}
range 遍历
```

###### range 会给每一个元素创建一个副本, 会有值拷贝, 如果数组存的是大的结构体可以用 index 遍历或者指针优化

因为 value 是副本, 所以不能修改原有的值

###### append 会改变地址

slice 类型的本质是一个结构体

```plain
type slice struct {
 array unsafe.Pointer
 len   int
 cap   int
}
```

函数的值拷贝会导致修改失效

```plain
func TestAppend1(t *testing.T) {
 var a []int
 add(a)
 println(len(a))
 // 0
}

func add(a []int) {
 a = append(a, 1)
}
```

##### 并发下 go 函数闭包问题

```plain
for i := 0; i < 3; i++ {
    go func() {
        println(i)
    }()
}
time.Sleep(time.Second)
// 2
// 2
// 2
```

说明: 因为闭包导致 i 变量逃逸到堆空间, 所有的 go 共用了 i 变量, 导致并发问题

###### 解决方法1: 局部变量

```plain
for i := 0; i < 3; i++ {
    ii := i
    go func() {
        println(ii)
    }()
}
time.Sleep(time.Second)
// 2
// 0
// 1
```

###### 解决方法2: 参数传递

```plain
for i := 0; i < 3; i++ {
    go func(ii int) {
        println(ii)
    }(i)
}
time.Sleep(time.Second)
// 2
// 0
// 1
```

##### buffer 之坑

###### buffer 对象池一定要用完才还回去, 不然buffer在多处复用导致底层的 []byte 内容不一致

###### 使用buffer pool

** 参考 ** : [golang-buffer-pool](https://link.zhihu.com/?target=https%3A//saltbo.cn/posts/golang-buffer-pool.html)

我们的一个 httpClient 返回处理使用了 sync.pool 缓存 buffer, 测试是内存优化了6-8倍，后面测试的时候发现, 获取的内容会偶尔不一致, review 代码发现可能是并发时候 buffer 指针放回去了还在使用, 导致和buffer pool 里面不一致 首先考虑就是将 buffer 的 bytes 读取出来, 然后再 put 回池子里面 然后 bytes 是一个切片, 底层还是和 buffer 共用一个 []byte, buffer 再次修改的时候底层的 []byte 也会被修改, 导致状态不一致 这些理论上是并发问题, 但是我们测试发现, 单线程调用 httpClient 时候, 有时候会有问题, 有时候又没有问题 官方的 http client 做请求的时候会开一个协程, sync pool在同一个协程下面复用对象是一致的, 但是多协程就会新建, 会尝试通过协程的id获取与之对应的对象, 没有才去新建. 串行执行请求也会产生多个协程, 所以偶尔会触发新建 sync 的buffer, 如果新建就不会报错, 如果不新建就会报错.

##### for select default 之坑

###### for 中的 default 在 select 一定会执行, CPU 一直被占用不会让出, 导致 CPU 空转

```plain
func TestForSelect(t *testing.T) {
 for {
  select {
  case <-time.After(time.Second * 1):
   println("hello")
  default:
   if math.Pow10(100) == math.Pow(10, 100) {
    println("equal")
   }
  }
 }
}
```

```plain
top - 15:00:50 up 1 day, 15:55,  0 users,  load average: 1.36, 0.85, 0.35
  PID USER      PR  NI    VIRT    RES    SHR S  %CPU  %MEM     TIME+ COMMAND
28632 root      20   0 2168296   1.4g   2244 S 252.8  11.7   1:04.15 __debug_bin
```

/

## 注意break作用域

比方说：

```go
for i := 0; i < 5; i++ {
      fmt.Printf("%d ", i)

      switch i {
      default:
      case 2:
              break
      }
  }
```

上面这个代码本来想 break 停止遍历，实际上只是break 了 switch 作用域，print 依然会打印：0，1，2，3，4。

正确做法应该是通过 label 的方式break：

```go
loop:
    for i := 0; i < 5; i++ {
        fmt.Printf("%d ", i)
        switch i {
        default:
        case 2:
            break loop
        }
    }
```

有时候我们会没注意到自己的错误用法，比如下面：

```go
for {
        select {
        case <-ch:
            // Do something
        case <-ctx.Done():
            break
        }
    }
```

上面这种写法会导致只跳出了 select，并没有终止for循环，正确写法应该这样：

```go
loop:
    for {
        select {
        case <-ch:
            // Do something
        case <-ctx.Done():
            break loop
        }
    }
```

####
