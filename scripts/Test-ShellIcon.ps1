# Windows validation only. The application uses native Go/Win32, not this script.
[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][string]$ExecutablePath,
    [Parameter(Mandatory=$true)][string]$ShortcutPath,
    [Parameter(Mandatory=$true)][string]$ExpectedIconPath
)
$ErrorActionPreference='Stop'
Add-Type -AssemblyName System.Drawing
Add-Type -ReferencedAssemblies System.Drawing -TypeDefinition @'
using System;
using System.Drawing;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
public static class SicmShellIconProbe {
    [StructLayout(LayoutKind.Sequential,CharSet=CharSet.Unicode)]
    struct Info {
        public IntPtr Icon; public int Index; public uint Attributes;
        [MarshalAs(UnmanagedType.ByValTStr,SizeConst=260)] public string Name;
        [MarshalAs(UnmanagedType.ByValTStr,SizeConst=80)] public string Type;
    }
    [DllImport("shell32.dll",CharSet=CharSet.Unicode)]
    static extern IntPtr SHGetFileInfo(string path,uint attributes,ref Info info,uint size,uint flags);
    [DllImport("user32.dll")] static extern bool DestroyIcon(IntPtr icon);
    public static string Pixels(string path,bool shortcutOverlay) {
        Info info=new Info();
        uint flags=shortcutOverlay ? 0x8100u : 0x100u;
        if(SHGetFileInfo(path,0,ref info,(uint)Marshal.SizeOf(typeof(Info)),flags)==IntPtr.Zero || info.Icon==IntPtr.Zero)
            throw new Exception("Shell icon lookup failed: "+path);
        try {
            using(Icon icon=Icon.FromHandle(info.Icon))
            using(Bitmap bitmap=icon.ToBitmap()) {
                byte[] pixels=new byte[bitmap.Width*bitmap.Height*4+8];
                Buffer.BlockCopy(BitConverter.GetBytes(bitmap.Width),0,pixels,0,4);
                Buffer.BlockCopy(BitConverter.GetBytes(bitmap.Height),0,pixels,4,4);
                int offset=8;
                for(int y=0;y<bitmap.Height;y++) for(int x=0;x<bitmap.Width;x++) {
                    Color c=bitmap.GetPixel(x,y);
                    pixels[offset++]=c.R;pixels[offset++]=c.G;pixels[offset++]=c.B;pixels[offset++]=c.A;
                }
                using(SHA256 sha=SHA256.Create()) return BitConverter.ToString(sha.ComputeHash(pixels)).Replace("-","").ToLowerInvariant();
            }
        } finally { DestroyIcon(info.Icon); }
    }
}
'@
$expected=[SicmShellIconProbe]::Pixels([IO.Path]::GetFullPath($ExpectedIconPath),$false)
$expectedShortcut=[SicmShellIconProbe]::Pixels([IO.Path]::GetFullPath($ExpectedIconPath),$true)
$executable=[SicmShellIconProbe]::Pixels([IO.Path]::GetFullPath($ExecutablePath),$false)
$shortcut=[SicmShellIconProbe]::Pixels([IO.Path]::GetFullPath($ShortcutPath),$false)
if($executable -ne $expected){throw 'Explorer executable icon pixels do not match the approved ICO.'}
if($shortcut -ne $expectedShortcut){throw 'Desktop shortcut icon pixels do not match the approved ICO with the native shortcut arrow.'}
ConvertTo-Json -Compress -InputObject @{status='passed';pixel_sha256=$expected;shortcut_pixel_sha256=$expectedShortcut;native_shortcut_arrow=$true;executable_matches=$true;shortcut_matches=$true}
