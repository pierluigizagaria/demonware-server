"""Bounded, synthetic reproduction of the owned game's CommonAddr ID gate.

Requires the workspace OrbisImage inspector, Unicorn, and a trusted local
librhash. No process, network, runtime profile, or captured packet is accessed.
Only selected original instructions run in the CPU emulator. The digest uses
librhash; this is not a complete execution of the game's hash implementation.
"""

import argparse
import base64
import ctypes
import hashlib
import importlib.util
import json
from pathlib import Path
import struct
import sys

from unicorn import Uc, UC_ARCH_X86, UC_MODE_64, UC_HOOK_CODE
from unicorn.x86_const import (
    UC_X86_REG_R12, UC_X86_REG_RBP, UC_X86_REG_RBX, UC_X86_REG_RSP,
)


EXPECTED_IMAGE = "e3b7a068b5e7be0f976102aa223a74425e7817c67e26e21416163291750c1b83"
SENTINEL = bytes((0, 255, 0, 255))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True, type=Path)
    parser.add_argument("--inspector", required=True, type=Path)
    parser.add_argument("--rhash", required=True, type=Path)
    parser.add_argument("--documents-stdin", action="store_true",
                        help="also check synthetic self/advertised/other CommonAddr documents from stdin")
    args = parser.parse_args()
    data = args.image.read_bytes()
    if hashlib.sha256(data).hexdigest() != EXPECTED_IMAGE:
        parser.error("unsupported owned image hash")
    spec = importlib.util.spec_from_file_location("orbis_inspector", args.inspector)
    inspector = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(inspector)
    image = inspector.OrbisImage(data)
    # The owned default address initializer is mov dword [rdi], 0xff00ff00.
    assert image.read_vaddr(0x191080, 6) == b"\xc7\x07" + SENTINEL
    assert image.read_vaddr(0x185B8E + 0x1A6C95A, 6) == b"tiger\0"

    rhash = ctypes.CDLL(str(args.rhash.resolve()))
    rhash.rhash_library_init()
    rhash.rhash_msg.argtypes = [ctypes.c_uint, ctypes.c_void_p,
                                ctypes.c_size_t, ctypes.c_void_p]
    rhash.rhash_msg.restype = ctypes.c_int

    def tiger_id(endpoint):
        digest = (ctypes.c_ubyte * 24)()
        assert rhash.rhash_msg(0x10, endpoint, len(endpoint), digest) == 0
        return bytes(digest)[:4]

    def original_public_selection(public):
        machine = Uc(UC_ARCH_X86, UC_MODE_64)
        machine.mem_map(0x185000, 0x1000)
        machine.mem_map(0x500000, 0x2000)
        machine.mem_write(0x185AC7, image.read_vaddr(0x185AC7, 10))
        machine.mem_write(0x500000, public)
        machine.mem_write(0x500F50, SENTINEL)
        machine.reg_write(UC_X86_REG_RBX, 0x500000)
        machine.reg_write(UC_X86_REG_RBP, 0x501000)
        result = []

        def stop(uc, address, _size, _context):
            if address in (0x185AD1, 0x185B01):
                result.append(address == 0x185B01)
                uc.emu_stop()

        machine.hook_add(UC_HOOK_CODE, stop)
        machine.emu_start(0x185AC7, 0x185B02, count=10)
        assert len(result) == 1
        return result[0]

    def identity(common):
        public = common[60:66]
        if original_public_selection(public):
            return tiger_id(public)
        first_local = next((common[i:i + 6] for i in range(0, 60, 6)
                            if common[i:i + 4] != SENTINEL), SENTINEL + b"\0\0")
        return tiger_id(first_local)

    def original_receive_gate(own_id, packet_id):
        machine = Uc(UC_ARCH_X86, UC_MODE_64)
        for address in (0x186000, 0x1BD000, 0x500000, 0x600000):
            machine.mem_map(address, 0x2000)
        machine.mem_write(0x1860D0, image.read_vaddr(0x1860D0, 16))
        machine.mem_write(0x1BD69F, image.read_vaddr(0x1BD69F, 0x17))
        machine.mem_write(0x500070, struct.pack("<Q", 0x500100))
        machine.mem_write(0x5001AC, own_id)
        machine.mem_write(0x500310, packet_id)
        machine.reg_write(UC_X86_REG_R12, 0x500000)
        machine.reg_write(UC_X86_REG_RBX, 0x500300)
        machine.reg_write(UC_X86_REG_RSP, 0x601000)
        result = []

        def stop(uc, address, _size, _context):
            if address in (0x1BD6B6, 0x1BDAC1):
                result.append(address == 0x1BD6B6)
                uc.emu_stop()

        machine.hook_add(UC_HOOK_CODE, stop)
        machine.emu_start(0x1BD69F, 0x1BDAC2, count=20)
        assert len(result) == 1
        return result[0]

    def endpoint(address, port):
        return bytes(address) + struct.pack("<H", port)

    empty = SENTINEL + b"\0\0"
    original = (endpoint((192, 0, 2, 10), 3074) + empty * 9
                + endpoint((198, 51, 100, 8), 45000) + bytes([3]))
    proxy = endpoint((127, 0, 0,10), 51000)
    rewritten = proxy * 11 + original[66:]
    auxiliary = original[:6] + proxy * 9 + original[60:]
    fallback = original[:60] + empty + original[66:]
    fallback_auxiliary = fallback[:6] + proxy * 9 + fallback[60:]
    loopback = (endpoint((127, 0, 0, 1), 3074) + empty * 9
                + endpoint((127, 64, 1, 2), 45000) + bytes([3]))
    loopback_other = (endpoint((127, 0, 0, 1), 3074) + empty * 9
                      + endpoint((127, 64, 1, 3), 45000) + bytes([3]))
    cases = {
        "all_slots_change_id": identity(original) != identity(rewritten),
        "owned_receiver_rejects_all_slots": not original_receive_gate(
            identity(original), identity(rewritten)),
        "owned_receiver_accepts_unchanged": original_receive_gate(
            identity(original), identity(original)),
        "auxiliary_preserves_id": identity(original) == identity(auxiliary),
        "owned_receiver_accepts_auxiliary": original_receive_gate(
            identity(original), identity(auxiliary)),
        "sentinel_public_falls_back": not original_public_selection(empty),
        "zero_ip_is_not_sentinel": original_public_selection(bytes(6)),
        "fallback_auxiliary_preserves_id": identity(fallback) == identity(fallback_auxiliary),
        "owned_receiver_accepts_fallback_auxiliary": original_receive_gate(
            identity(fallback), identity(fallback_auxiliary)),
        "nat_byte_preserved": rewritten[66] == auxiliary[66] == fallback_auxiliary[66] == 3,
        "loopback_public_is_identity": original_public_selection(loopback[60:66]),
        "loopback_canonical_ids_distinct": identity(loopback) != identity(loopback_other),
        "owned_receiver_accepts_loopback_canonical": original_receive_gate(
            identity(loopback), identity(loopback)),
        "owned_receiver_rejects_other_loopback": not original_receive_gate(
            identity(loopback), identity(loopback_other)),
    }
    if args.documents_stdin:
        documents = json.load(sys.stdin)
        own, advertised, other = (base64.b64decode(documents[name], validate=True)
                                  for name in ("self", "advertised", "other"))
        assert all(len(value) == 67 for value in (own, advertised, other))
        cases.update({
            "actual_advertisement_byte_exact": own == advertised,
            "actual_canonical_ids_distinct": identity(own) != identity(other),
            "owned_receiver_accepts_actual_advertisement": original_receive_gate(
                identity(own), identity(advertised)),
            "owned_receiver_rejects_other_canonical": not original_receive_gate(
                identity(own), identity(other)),
        })
    print(json.dumps({"ok": all(cases.values()), **cases}, sort_keys=True))
    if not all(cases.values()):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
