; UniFi Cert Smash Deck — NSIS Windows Installer
; Build with: makensis /DVERSION=1.0.0 windows.nsi
; Requires: unifi-cert-smash-deck.exe in the same directory as this script

Unicode True

!define APP_NAME      "UniFi Cert Smash Deck"
!define APP_EXE       "unifi-cert-smash-deck.exe"
!define APP_URL       "http://127.0.0.1:8105"
!define SERVICE_NAME  "UniFiCertSmashDeck"
!define PUBLISHER     "niski84"
!define REG_KEY       "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_NAME}"

; Version passed in from build: /DVERSION=x.y.z
!ifndef VERSION
  !define VERSION "dev"
!endif

Name              "${APP_NAME} ${VERSION}"
OutFile           "unifi-cert-smash-deck-${VERSION}-windows-amd64-setup.exe"
InstallDir        "$PROGRAMFILES64\${APP_NAME}"
InstallDirRegKey  HKLM "${REG_KEY}" "InstallLocation"
RequestExecutionLevel admin

; ── Compression ──────────────────────────────────────────────────────────────
SetCompressor     /SOLID lzma
SetCompressorDictSize 8

; ── Modern UI ────────────────────────────────────────────────────────────────
!include "MUI2.nsh"
!include "nsDialogs.nsh"

!define MUI_ABORTWARNING
!define MUI_ICON   "${NSISDIR}\Contrib\Graphics\Icons\modern-install.ico"
!define MUI_UNICON "${NSISDIR}\Contrib\Graphics\Icons\modern-uninstall.ico"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
Page custom ServicePage ServicePageLeave
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

; ── Service checkbox state ────────────────────────────────────────────────────
Var InstallService
Var ServiceCheckbox

Function ServicePage
  !insertmacro MUI_HEADER_TEXT "Windows Service" "Run as a background service (starts automatically with Windows)"
  nsDialogs::Create 1018
  Pop $0
  ${NSD_CreateCheckbox} 0 30u 100% 12u "Install and start as a Windows service (recommended)"
  Pop $ServiceCheckbox
  ${NSD_SetState} $ServiceCheckbox ${BST_CHECKED}
  nsDialogs::Show
FunctionEnd

Function ServicePageLeave
  ${NSD_GetState} $ServiceCheckbox $InstallService
FunctionEnd

; ── Install section ──────────────────────────────────────────────────────────
Section "Core" SecCore
  SectionIn RO

  SetOutPath "$INSTDIR"
  File "${APP_EXE}"

  ; Create data directory
  CreateDirectory "$INSTDIR\data"

  ; .env stub (user edits this)
  FileOpen  $0 "$INSTDIR\.env.example" w
  FileWrite $0 "PORT=8105$\r$\n"
  FileWrite $0 "# UNIFICERT_SSH_HOST=192.168.1.1$\r$\n"
  FileWrite $0 "# UNIFICERT_SSH_USER=root$\r$\n"
  FileWrite $0 "# UNIFICERT_SSH_KEY=C:\Users\you\.ssh\id_ed25519$\r$\n"
  FileWrite $0 "# UNIFICERT_CERT_EMAIL=you@example.com$\r$\n"
  FileWrite $0 "# UNIFICERT_CERT_HOSTS=unifi.example.com$\r$\n"
  FileWrite $0 "# UNIFICERT_DNS_PROVIDER=cloudflare$\r$\n"
  FileClose $0

  ; Launcher batch file (fallback / manual start)
  FileOpen  $0 "$INSTDIR\launch.bat" w
  FileWrite $0 "@echo off$\r$\n"
  FileWrite $0 "cd /d $\"%~dp0$\"$\r$\n"
  FileWrite $0 "start $\"$\" $\"${APP_EXE}$\"$\r$\n"
  FileWrite $0 "timeout /t 2 >nul$\r$\n"
  FileWrite $0 "start $\"$\" $\"${APP_URL}$\"$\r$\n"
  FileClose $0

  ; Start Menu shortcuts
  CreateDirectory "$SMPROGRAMS\${APP_NAME}"
  CreateShortcut  "$SMPROGRAMS\${APP_NAME}\Open Dashboard.lnk"  \
                  "$INSTDIR\launch.bat"                          \
                  ""                                             \
                  "$INSTDIR\${APP_EXE}" 0
  CreateShortcut  "$SMPROGRAMS\${APP_NAME}\Uninstall.lnk"        \
                  "$INSTDIR\Uninstall.exe"

  ; Uninstaller
  WriteUninstaller "$INSTDIR\Uninstall.exe"

  ; Add/Remove Programs entry
  WriteRegStr   HKLM "${REG_KEY}" "DisplayName"     "${APP_NAME}"
  WriteRegStr   HKLM "${REG_KEY}" "DisplayVersion"  "${VERSION}"
  WriteRegStr   HKLM "${REG_KEY}" "Publisher"       "${PUBLISHER}"
  WriteRegStr   HKLM "${REG_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr   HKLM "${REG_KEY}" "UninstallString" "$INSTDIR\Uninstall.exe"
  WriteRegDWORD HKLM "${REG_KEY}" "NoModify"        1
  WriteRegDWORD HKLM "${REG_KEY}" "NoRepair"        1
SectionEnd

; ── Optional: Windows service ────────────────────────────────────────────────
Section "Windows Service" SecService
  ${If} $InstallService == ${BST_CHECKED}
    ; Stop and remove any existing service first
    ExecWait 'sc stop "${SERVICE_NAME}"'
    ExecWait 'sc delete "${SERVICE_NAME}"'
    Sleep 1000

    ; Register new service
    ExecWait 'sc create "${SERVICE_NAME}" \
              binPath= "$INSTDIR\${APP_EXE}" \
              DisplayName= "${APP_NAME}" \
              start= auto \
              obj= LocalSystem'
    ExecWait 'sc description "${SERVICE_NAME}" "Let$\'s Encrypt cert manager for UniFi Dream Machine"'
    ExecWait 'sc start "${SERVICE_NAME}"'

    ; Open browser after short delay
    ExecShell "open" "${APP_URL}"
  ${EndIf}
SectionEnd

; ── Uninstall section ────────────────────────────────────────────────────────
Section "Uninstall"
  ; Stop and remove service
  ExecWait 'sc stop "${SERVICE_NAME}"'
  ExecWait 'sc delete "${SERVICE_NAME}"'
  Sleep 1000

  ; Remove files (preserve data/)
  Delete "$INSTDIR\${APP_EXE}"
  Delete "$INSTDIR\launch.bat"
  Delete "$INSTDIR\.env.example"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir  "$INSTDIR"

  ; Start menu
  Delete "$SMPROGRAMS\${APP_NAME}\Open Dashboard.lnk"
  Delete "$SMPROGRAMS\${APP_NAME}\Uninstall.lnk"
  RMDir  "$SMPROGRAMS\${APP_NAME}"

  ; Registry
  DeleteRegKey HKLM "${REG_KEY}"
SectionEnd
