# Sanitized recordings

`http/` contains one representative captured JSON exchange for device listing and detail, device settings reads/updates, siren on/off, history devices, a device timeline, and signaling ticket bootstrap. Request paths use placeholders for device and location identifiers; query pairs are retained with sensitive values scrubbed. HTTP fixture bodies use structured JSON values and the recorded response JSON flag.

`sessions/flow-21.json` and `sessions/flow-402.json` preserve the complete captured application-message order and direction from the two PTZ conversations. Protocol methods and enum values remain intact. Account, device, route, session, and command identities are replaced consistently within each conversation; dates, SDP ICE credentials, fingerprints, and network addresses are synthetic.

The JSON schemas are in `schemas/`. Rebuild the files from a private local mitmproxy dump with `python tools/capture/extract.py <capture-file>`; the source recording is not included.
