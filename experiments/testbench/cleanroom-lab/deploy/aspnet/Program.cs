// P19：最小 ASP.NET Core minimal API,与 Go 单文件服务对照(包体/启动/内存)。
var builder = WebApplication.CreateSlimBuilder(args);
var app = builder.Build();
app.MapGet("/api/health", () => Results.Json(new { ok = true, service = "gallery-aspnet" }));
app.MapGet("/api/works", () => Results.Json(new { items = new[] { "work-1", "work-2" } }));
app.Run("http://127.0.0.1:18097");
