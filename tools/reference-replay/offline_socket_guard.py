"""Pytest guard that permits Windows asyncio loopback socket pairs only."""

from __future__ import annotations

import ipaddress
import socket


_original_socket = socket.socket
_original_getaddrinfo = socket.getaddrinfo
_original_gethostbyname = socket.gethostbyname
_original_gethostbyname_ex = socket.gethostbyname_ex
_original_gethostbyaddr = socket.gethostbyaddr
_af_unix = getattr(socket, "AF_UNIX", None)


def _is_loopback(host: object) -> bool:
    if not isinstance(host, str):
        return False
    if host.lower() == "localhost":
        return True
    try:
        address = ipaddress.ip_address(host.split("%", 1)[0])
        return address.is_loopback or (
            address.version == 6
            and address.ipv4_mapped is not None
            and address.ipv4_mapped.is_loopback
        )
    except ValueError:
        return False


def _guarded_getaddrinfo(host, *args, **kwargs):
    if not _is_loopback(host):
        raise OSError(f"offline socket guard blocked name resolution for {host!r}")
    return _original_getaddrinfo(host, *args, **kwargs)


def _guarded_gethostbyname(host):
    if not _is_loopback(host):
        raise OSError(f"offline socket guard blocked name resolution for {host!r}")
    return _original_gethostbyname(host)


def _guarded_gethostbyname_ex(host):
    if not _is_loopback(host):
        raise OSError(f"offline socket guard blocked name resolution for {host!r}")
    return _original_gethostbyname_ex(host)


def _guarded_gethostbyaddr(host):
    if not _is_loopback(host):
        raise OSError(f"offline socket guard blocked reverse lookup for {host!r}")
    return _original_gethostbyaddr(host)


class _LoopbackSocket(_original_socket):
    def connect(self, address):
        if self.family != _af_unix and not _is_loopback(address[0] if isinstance(address, tuple) else address):
            raise OSError(f"offline socket guard blocked connection to {address!r}")
        return super().connect(address)

    def connect_ex(self, address):
        if self.family != _af_unix and not _is_loopback(address[0] if isinstance(address, tuple) else address):
            raise OSError(f"offline socket guard blocked connection to {address!r}")
        return super().connect_ex(address)

    def sendto(self, data, *args):
        address = args[-1]
        if self.family != _af_unix and not _is_loopback(address[0] if isinstance(address, tuple) else address):
            raise OSError(f"offline socket guard blocked datagram to {address!r}")
        return super().sendto(data, *args)


def pytest_configure(config):
    socket.socket = _LoopbackSocket
    socket.getaddrinfo = _guarded_getaddrinfo
    socket.gethostbyname = _guarded_gethostbyname
    socket.gethostbyname_ex = _guarded_gethostbyname_ex
    socket.gethostbyaddr = _guarded_gethostbyaddr


def pytest_unconfigure(config):
    socket.socket = _original_socket
    socket.getaddrinfo = _original_getaddrinfo
    socket.gethostbyname = _original_gethostbyname
    socket.gethostbyname_ex = _original_gethostbyname_ex
    socket.gethostbyaddr = _original_gethostbyaddr
