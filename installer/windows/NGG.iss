; NGG Next-Generation Gateway - Windows installer (Inno Setup 6)
;
; Build:
;   Just run installer\windows\build-installer.ps1 -Version v1.0.0 -StageDir <stage>.
;   It generates NGG.build.iss with concrete #define values (ISCC 6.7 has no
;   /D switch), calls ISCC, then removes the temporary file. Without -StageDir
;   it builds the stage from source first.
;
; StageDir layout (produced by build-installer.ps1 or the release tarball):
;   nggd.exe, web-dist\*, configs\*, docs\*, scripts\*

#ifndef MyAppVersion
  #define MyAppVersion "dev"
#endif
#ifndef StageDir
  #define StageDir "..\..\dist\ngg"
#endif

#define MyAppName "NGG Next-Generation Gateway"
#define MyAppPublisher "welkacton"
#define MyAppURL "https://github.com/welkacton/ngg-next-generation-gateway"

[Setup]
AppId={{9C4B7E21-5A83-4D6F-B1E0-73A2F8C5D914}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}/issues
AppUpdatesURL={#MyAppURL}/releases
DefaultDirName={autopf}\NGG
DefaultGroupName=NGG
DisableProgramGroupPage=no
LicenseFile=..\..\LICENSE
OutputBaseFilename=NGG-Setup-{#MyAppVersion}
Compression=lzma2
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=admin
WizardStyle=modern
CloseApplications=yes
UninstallDisplayIcon={app}\nggd.exe

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Additional shortcuts:"; Flags: unchecked

[Files]
Source: "{#StageDir}\nggd.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\web-dist\*"; DestDir: "{app}\web\dist"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#StageDir}\configs\*"; DestDir: "{app}\configs"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#StageDir}\docs\*"; DestDir: "{app}\docs"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#StageDir}\scripts\*"; DestDir: "{app}\scripts"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "install.ps1"; DestDir: "{app}\installer"; Flags: ignoreversion
Source: "uninstall.ps1"; DestDir: "{app}\installer"; Flags: ignoreversion
Source: "install.bat"; DestDir: "{app}\installer"; Flags: ignoreversion
Source: "set-system-proxy.ps1"; DestDir: "{app}\installer"; Flags: ignoreversion

[Icons]
Name: "{group}\NGG Web UI"; Filename: "http://127.0.0.1:9090/"
Name: "{group}\NGG Access Info"; Filename: "{app}\NGG-ACCESS.txt"; IconFilename: "{sys}\notepad.exe"
Name: "{group}\Uninstall NGG"; Filename: "{uninstallexe}"
Name: "{autodesktop}\NGG Web UI"; Filename: "http://127.0.0.1:9090/"; Tasks: desktopicon

[Run]
; install.ps1 writes configs/ngg.yaml (absolute paths + random token), registers
; the NGG Windows service, starts it, verifies /api/v1/health, opens the Web UI.
Filename: "{sys}\WindowsPowerShell\v1.0\powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\installer\install.ps1"" -InstallDir ""{app}"" -ConfigureOnly -Silent -OpenBrowser"; Flags: runhidden; StatusMsg: "Configuring the NGG Windows service..."

[UninstallRun]
; KeepData: runtime-generated config/data/logs are user data and stay on disk.
Filename: "{sys}\WindowsPowerShell\v1.0\powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\installer\uninstall.ps1"" -InstallDir ""{app}"" -Silent -KeepData"; Flags: runhidden; RunOnceId: "StopNGGService"
