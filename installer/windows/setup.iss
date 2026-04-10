; lark-agent-bridge Windows Installer
; Inno Setup Script
; 构建方法: 在 Windows 上安装 Inno Setup，打开此文件编译

#define MyAppName "Lark Agent Bridge"
#define MyAppVersion "1.0.0"
#define MyAppPublisher "LJW0401"
#define MyAppURL "https://github.com/LJW0401/lark-agent-bridge"
#define MyAppExeName "lark-agent-bridge.exe"

[Setup]
AppId={{B8F3A1E2-5C4D-4E6F-8A7B-9C0D1E2F3A4B}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}/issues
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
AllowNoIcons=yes
OutputDir=..\..\build
OutputBaseFilename=lark-agent-bridge-setup
Compression=lzma
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64compatible
SetupIconFile=icon.ico
UninstallDisplayIcon={app}\{#MyAppExeName}

[Languages]
Name: "chinesesimplified"; MessagesFile: "compiler:Languages\ChineseSimplified.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
; 主程序
Source: "..\..\build\lark-agent-bridge_windows_amd64.exe"; DestDir: "{app}"; DestName: "{#MyAppExeName}"; Flags: ignoreversion
; 配置模板
Source: "..\..\config.example.yaml"; DestDir: "{app}"; DestName: "config.example.yaml"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\配置文件"; Filename: "{app}\config.yaml"
Name: "{group}\卸载 {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{commondesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "附加选项:"
Name: "installservice"; Description: "安装为 Windows 系统服务（开机自启）"; GroupDescription: "服务选项:"; Flags: checkedonce
Name: "addtopath"; Description: "将安装目录添加到系统 PATH"; GroupDescription: "附加选项:"; Flags: checkedonce

[Run]
; 安装后复制配置模板
Filename: "cmd.exe"; Parameters: "/c copy ""{app}\config.example.yaml"" ""{app}\config.yaml"""; Flags: runhidden; Check: not ConfigExists
; 安装为 Windows 服务
Filename: "{app}\{#MyAppExeName}"; Parameters: "install"; StatusMsg: "正在注册系统服务..."; Tasks: installservice; Flags: runhidden waituntilterminated
; 启动服务
Filename: "{app}\{#MyAppExeName}"; Parameters: "start"; StatusMsg: "正在启动服务..."; Tasks: installservice; Flags: runhidden waituntilterminated

[UninstallRun]
; 卸载前停止并移除服务
Filename: "{app}\{#MyAppExeName}"; Parameters: "stop"; Flags: runhidden waituntilterminated
Filename: "{app}\{#MyAppExeName}"; Parameters: "uninstall"; Flags: runhidden waituntilterminated

[Registry]
; 添加到 PATH
Root: HKLM; Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; Tasks: addtopath; Check: NeedsAddPath('{app}')

[Code]
// 检查配置文件是否已存在
function ConfigExists(): Boolean;
begin
  Result := FileExists(ExpandConstant('{app}\config.yaml'));
end;

// 检查 PATH 中是否已包含安装目录
function NeedsAddPath(Param: string): boolean;
var
  OrigPath: string;
begin
  if not RegQueryStringValue(HKEY_LOCAL_MACHINE,
    'SYSTEM\CurrentControlSet\Control\Session Manager\Environment',
    'Path', OrigPath)
  then begin
    Result := True;
    exit;
  end;
  Result := Pos(';' + Param + ';', ';' + OrigPath + ';') = 0;
end;

// 卸载时清理 PATH
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  OrigPath: string;
  NewPath: string;
  AppDir: string;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    AppDir := ExpandConstant('{app}');
    if RegQueryStringValue(HKEY_LOCAL_MACHINE,
      'SYSTEM\CurrentControlSet\Control\Session Manager\Environment',
      'Path', OrigPath)
    then begin
      NewPath := OrigPath;
      StringChangeEx(NewPath, ';' + AppDir, '', True);
      StringChangeEx(NewPath, AppDir + ';', '', True);
      StringChangeEx(NewPath, AppDir, '', True);
      if NewPath <> OrigPath then
        RegWriteExpandStringValue(HKEY_LOCAL_MACHINE,
          'SYSTEM\CurrentControlSet\Control\Session Manager\Environment',
          'Path', NewPath);
    end;
  end;
end;
