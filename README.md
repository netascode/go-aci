[![Tests](https://github.com/netascode/go-aci/actions/workflows/test.yml/badge.svg)](https://github.com/netascode/go-aci/actions/workflows/test.yml)

# go-aci

`go-aci` is a Go client library for Cisco ACI. It is based on Nathan's excellent [goaci](https://github.com/brightpuddle/goaci) module and features a simple, extensible API and [advanced JSON manipulation](#result-manipulation).

## Getting Started

### Installing

To start using `go-aci`, install Go and `go get`:

`$ go get -u github.com/netascode/go-aci`

### Basic Usage

```go
package main

import "github.com/netascode/go-aci"

func main() {
    client, _ := aci.NewClient("https://1.1.1.1", "user", "pwd")

    res, _ = client.Get("/api/mo/uni/tn-infra")
    println(res.Get("imdata.0.*.attributes.name"))
}
```

This will print:

```
infra
```

#### Result manipulation

`aci.Result` uses GJSON to simplify handling JSON results. See the [GJSON](https://github.com/tidwall/gjson) documentation for more detail.

```go
res, _ := client.GetClass("fvBD")
res.Get("0.fvBD.attributes.name").Str // name of first BD
res.Get("0.*.attributes.name").Str // name of first BD (if you don't know the class)

for _, bd := range res.Array() {
    println(res.Get("*.attributes|@pretty")) // pretty print BD attributes
}

for _, bd := range res.Get("#.fvBD.attributes").Array() {
    println(res.Get("@pretty")) // pretty print BD attributes
}
```

#### Helpers for common patterns

```go
res, _ := client.GetDn("uni/tn-infra")
res, _ := client.GetClass("fvTenant")
```

#### Query parameters

Pass the `aci.Query` object to the `Get` request to add query paramters:

```go
queryInfra := aci.Query("query-target-filter", `eq(fvTenant.name,"infra")`)
res, _ := client.GetClass("fvTenant", queryInfra)
```

Pass as many paramters as needed:

```go
res, _ := client.GetClass("isisRoute",
    aci.Query("rsp-subtree-include", "relations"),
    aci.Query("query-target-filter", `eq(isisRoute.pfx,"10.66.0.1/32")`),
)
```

#### POST data creation

`aci.Body` is a wrapper for [SJSON](https://github.com/tidwall/sjson). SJSON supports a path syntax simplifying JSON creation.

```go
exampleTenant := aci.Body{}.Set("fvTenant.attributes.name", "aci-example").Str
client.Post("/api/mo/uni/tn-aci-example", exampleTenant)
```

These can be chained:

```go
tenantA := aci.Body{}.
    Set("fvTenant.attributes.name", "aci-example-a").
    Set("fvTenant.attributes.descr", "Example tenant A")
```

...or nested:

```go
attrs := aci.Body{}.
    Set("name", "aci-example-b").
    Set("descr", "Example tenant B").
    Str
tenantB := aci.Body{}.SetRaw("fvTenant.attributes", attrs).Str
```

#### Token refresh

Token refresh is handled automatically. The client keeps a timer and checks elapsed time on each request, refreshing the token every 8 minutes. This can be handled manually if desired:

```go
res, _ := client.Get("/api/...", aci.NoRefresh)
client.Refresh()
```

## Documentation

See the [documentation](https://godoc.org/github.com/netascode/go-aci) for more details.
