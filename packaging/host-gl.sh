#!/bin/sh
# Point a Nix-built GWorX at the host OpenGL stack when it is not running on NixOS.
#
# Nix's libglvnd looks for GPU drivers in /run/opengl-driver, which only exists
# on NixOS, so GLFW reports "EGL: Failed to get EGL display: Success" on other
# distros (issues #5 and #7). The profile packages and the AppImage source this
# script. LD_LIBRARY_PATH is searched before DT_RUNPATH.
#
# GWORX_NIX_LIB_PATH must list Nix libc and Nix libglvnd before any host
# directory. The binary is linked against Nix glibc, and a newer host libEGL
# or Mesa libgallium can require GLIBC symbols that Nix does not provide.
# Nix's libEGL is therefore the one that loads. The vendor JSON names the GPU
# driver by soname (libEGL_nvidia.so.0); that file is found only because the
# host library directory is later on LD_LIBRARY_PATH. Setting
# __EGL_VENDOR_LIBRARY_DIRS by itself leaves the soname inside the Nix store
# search and the window never opens.
#
#   GWORX_REAL          executable to run (the real ELF, not this script)
#   GWORX_NIX_LIB_PATH  Nix libc, libstdc++, libgcc, then Nix libglvnd
#   GWORX_GL_ROOT       filesystem root to inspect (default /; tests set this)
#   GWORX_GL_ECHO=1     print the environment and do not exec

set -eu

root="${GWORX_GL_ROOT:-/}"
root="${root%/}"
if [ -z "$root" ]; then
	root=/
fi

nixos=0
if [ -e "$root/etc/NIXOS" ] || [ -e "$root/run/opengl-driver/lib/libGL.so.1" ] || [ -e "$root/run/opengl-driver/lib/libEGL.so.1" ]; then
	nixos=1
fi

if [ "$nixos" -eq 0 ]; then
	host_libs=""
	add_host() {
		dir="$1"
		if [ ! -d "$dir" ]; then
			return 0
		fi
		if [ -e "$dir/libEGL.so.1" ] || [ -e "$dir/libGL.so.1" ]; then
			host_libs="${host_libs:+$host_libs:}$dir"
		fi
	}

	multi=""
	case "$(uname -m)" in
	x86_64) multi=x86_64-linux-gnu ;;
	aarch64 | arm64) multi=aarch64-linux-gnu ;;
	esac
	if [ -n "$multi" ]; then
		add_host "$root/usr/lib/$multi"
		add_host "$root/lib/$multi"
	fi
	add_host "$root/usr/lib64"
	add_host "$root/lib64"
	add_host "$root/usr/lib"
	add_host "$root/lib"

	ld="${GWORX_NIX_LIB_PATH:-}"
	if [ -n "$host_libs" ]; then
		ld="${ld:+$ld:}$host_libs"
	fi
	if [ -n "${LD_LIBRARY_PATH:-}" ]; then
		ld="${ld:+$ld:}$LD_LIBRARY_PATH"
	fi
	if [ -n "$ld" ]; then
		LD_LIBRARY_PATH="$ld"
		export LD_LIBRARY_PATH
	fi

	if [ -z "${__EGL_VENDOR_LIBRARY_FILENAMES:-}" ] && [ -z "${__EGL_VENDOR_LIBRARY_DIRS:-}" ]; then
		egl=""
		add_egl() {
			if [ -d "$1" ]; then
				egl="${egl:+$egl:}$1"
			fi
		}
		add_egl "$root/usr/share/glvnd/egl_vendor.d"
		if [ -n "$multi" ]; then
			add_egl "$root/usr/lib/$multi/GL/glvnd/egl_vendor.d"
		fi
		add_egl "$root/usr/lib64/GL/glvnd/egl_vendor.d"
		add_egl "$root/usr/lib/GL/glvnd/egl_vendor.d"
		if [ -n "$egl" ]; then
			__EGL_VENDOR_LIBRARY_DIRS="$egl"
			export __EGL_VENDOR_LIBRARY_DIRS
		fi
	fi

	if [ -z "${LIBGL_DRIVERS_PATH:-}" ]; then
		dri=""
		add_dri() {
			if [ -d "$1" ]; then
				dri="${dri:+$dri:}$1"
			fi
		}
		if [ -n "$multi" ]; then
			add_dri "$root/usr/lib/$multi/dri"
		fi
		add_dri "$root/usr/lib64/dri"
		add_dri "$root/usr/lib/dri"
		if [ -n "$dri" ]; then
			LIBGL_DRIVERS_PATH="$dri"
			export LIBGL_DRIVERS_PATH
		fi
	fi

	if [ -z "$host_libs" ]; then
		echo "gworx: no host libEGL or libGL under $root. Install the distro OpenGL and Wayland packages or the window cannot open." >&2
	fi
fi

if [ "${GWORX_GL_ECHO:-}" = 1 ]; then
	printf 'LD_LIBRARY_PATH=%s\n' "${LD_LIBRARY_PATH-}"
	printf '__EGL_VENDOR_LIBRARY_DIRS=%s\n' "${__EGL_VENDOR_LIBRARY_DIRS-}"
	printf 'LIBGL_DRIVERS_PATH=%s\n' "${LIBGL_DRIVERS_PATH-}"
	printf 'GWORX_REAL=%s\n' "${GWORX_REAL-}"
	exit 0
fi

if [ -z "${GWORX_REAL:-}" ]; then
	echo "gworx: GWORX_REAL is not set" >&2
	exit 127
fi
exec "$GWORX_REAL" "$@"
