# 生成 Windows 图标资源 rsrc_windows_amd64.syso（Go 编译时自动链接进 exe）
# 用法：python3 assets/make_syso.py assets/icon.ico rsrc_windows_amd64.syso
import struct, sys

ico = open(sys.argv[1], 'rb').read()
_, _, count = struct.unpack_from('<HHH', ico, 0)
images = []
for i in range(count):
    w, h, cc, _, planes, bpp, size, off = struct.unpack_from('<BBBBHHII', ico, 6 + 16 * i)
    images.append((w, h, cc, planes, bpp, ico[off:off + size]))

RT_ICON, RT_GROUP_ICON, LANG = 3, 14, 0x0409
group = struct.pack('<HHH', 0, 1, len(images))
for i, (w, h, cc, planes, bpp, data) in enumerate(images):
    group += struct.pack('<BBBBHHIH', w, h, cc, 0, planes, bpp, len(data), i + 1)

# 资源：[(type, id, data)]
res = [(RT_ICON, i + 1, img[5]) for i, img in enumerate(images)] + [(RT_GROUP_ICON, 1, group)]
types = sorted(set(r[0] for r in res))

def dir_hdr(n):
    return struct.pack('<IIHHHH', 0, 0, 0, 0, 0, n)

# 先算布局：根目录 → 每种类型的目录 → 每个 id 的语言目录 → 数据项 → 数据
root_size = 16 + 8 * len(types)
type_dirs, id_dirs = {}, {}
off = root_size
for t in types:
    items = [r for r in res if r[0] == t]
    type_dirs[t] = off
    off += 16 + 8 * len(items)
for r in res:
    id_dirs[(r[0], r[1])] = off
    off += 16 + 8
entries = {}
for r in res:
    entries[(r[0], r[1])] = off
    off += 16
datas = {}
for r in res:
    off = (off + 7) & ~7
    datas[(r[0], r[1])] = off
    off += len(r[2])
total = off

buf = bytearray(total)
relocs = []
struct.pack_into('<IIHHHH', buf, 0, 0, 0, 0, 0, 0, len(types))
for i, t in enumerate(types):
    struct.pack_into('<II', buf, 16 + 8 * i, t, 0x80000000 | type_dirs[t])
    items = [r for r in res if r[0] == t]
    struct.pack_into('<IIHHHH', buf, type_dirs[t], 0, 0, 0, 0, 0, len(items))
    for j, r in enumerate(items):
        struct.pack_into('<II', buf, type_dirs[t] + 16 + 8 * j, r[1], 0x80000000 | id_dirs[(t, r[1])])
for r in res:
    k = (r[0], r[1])
    struct.pack_into('<IIHHHH', buf, id_dirs[k], 0, 0, 0, 0, 0, 1)
    struct.pack_into('<II', buf, id_dirs[k] + 16, LANG, entries[k])
    struct.pack_into('<IIII', buf, entries[k], datas[k], len(r[2]), 0, 0)
    relocs.append(entries[k])  # OffsetToData 要在链接时加上 .rsrc 的 RVA
    buf[datas[k]:datas[k] + len(r[2])] = r[2]

HDR, SEC = 20, 40
raw_off = HDR + SEC
reloc_off = raw_off + total
sym_off = reloc_off + 10 * len(relocs)
out = struct.pack('<HHIIIHH', 0x8664, 1, 0, sym_off, 1, 0, 0)
out += struct.pack('<8sIIIIIIHHI', b'.rsrc', 0, 0, total, raw_off, reloc_off, 0, len(relocs), 0, 0x40000040)
out += bytes(buf)
for r in relocs:
    out += struct.pack('<IIH', r, 0, 3)  # IMAGE_REL_AMD64_ADDR32NB
out += struct.pack('<8sIhHBB', b'.rsrc', 0, 1, 0, 3, 0)
out += struct.pack('<I', 4)
open(sys.argv[2], 'wb').write(out)
print('ok', len(out), 'bytes,', len(images), 'icons')
