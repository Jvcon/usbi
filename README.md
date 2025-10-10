# usbi

Multi-platfrom USB Info CLI Tool

## 跨平台封装思路  

```plaintext
usbinfo/
 ├── backend_darwin.go     // system_profiler
 ├── backend_linux.go      // sysfs + udev + 可选UCSI
 ├── backend_windows.go    // SetupAPI + WinUSB
 ├── common.go             // 统一数据结构 & 输出逻辑
 └── main.go               // 主程序入口
```

| 系统        | 原生能力 | 可获取线缆信息 | 额外工具需求 |
|-------------|----------|----------------|--------------|
| macOS       | system_profiler SPUSBHostDataType | 是（USB速率、电源） | 无 |
| Linux       | sysfs + UCSI（若启用） + lsusb | 是（速率、功率，Type-C能力） | 某些功能需 UCSI |
| Windows     | SetupAPI + WinUSB | 速率、功率，但线缆规格有限 | 厂商驱动或 USB4 管理器可补充 |