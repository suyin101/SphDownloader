@echo off
chcp 65001 >nul
echo 如果下载器被强制关掉后上不了网，运行这个就能恢复。
reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings" /v ProxyEnable /t REG_DWORD /d 0 /f >nul
echo 已关闭系统代理，网络应该恢复了。
echo （如果你平时用加速器/梯子，重新打开它一次即可）
pause
