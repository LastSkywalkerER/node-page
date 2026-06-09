; NSIS installer for node-stats (Windows). Built in CI with:
;   makensis -DVERSION=x.y.z -DOUTFILE=...\node-stats_setup.exe windows-installer.nsi
; Expects node-stats.exe next to this script's working dir.
!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef OUTFILE
  !define OUTFILE "node-stats_setup.exe"
!endif
; File resolves relative to THIS script's dir, so the build passes the built
; binary's absolute path via -DEXE=...
!ifndef EXE
  !define EXE "node-stats.exe"
!endif

Name "node-stats ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\node-stats"
InstallDirRegKey HKLM "Software\node-stats" "InstallDir"
RequestExecutionLevel admin
ShowInstDetails show
ShowUninstDetails show

Page directory
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

Section "node-stats"
  SetOutPath "$INSTDIR"
  File "/oname=node-stats.exe" "${EXE}"
  WriteRegStr HKLM "Software\node-stats" "InstallDir" "$INSTDIR"
  ; Add/Remove Programs entry
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\node-stats" "DisplayName" "node-stats"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\node-stats" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\node-stats" "UninstallString" "$INSTDIR\uninstall.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortcut "$SMPROGRAMS\node-stats.lnk" "$INSTDIR\node-stats.exe"
SectionEnd

Section "Uninstall"
  Delete "$SMPROGRAMS\node-stats.lnk"
  Delete "$INSTDIR\node-stats.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\node-stats"
  DeleteRegKey HKLM "Software\node-stats"
SectionEnd
