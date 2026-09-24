#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import os
import plistlib
import sys

if len(sys.argv) != 2:
    raise SystemExit(f"usage: {sys.argv[0]} Info.plist")

with open(sys.argv[1], "rb") as source:
    info = plistlib.load(source)
info.update({
    "SUFeedURL": os.environ["SPARKLE_FEED_URL"],
    "SUPublicEDKey": os.environ["SPARKLE_PUBLIC_ED_KEY"],
    "SUEnableAutomaticChecks": True,
    "SURequireSignedFeed": True,
    "SUVerifyUpdateBeforeExtraction": True,
})
with open(sys.argv[1], "wb") as destination:
    plistlib.dump(info, destination, sort_keys=False)
