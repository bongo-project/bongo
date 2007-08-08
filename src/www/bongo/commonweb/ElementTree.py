## Helper functions for use with (c)ElementTree.
#
# http://effbot.org/zone/element-namespaces.htm

import cElementTree as ET

## Explicitly sets the namespace attributes on an Element.
#  @param elem The Element instance to set attributes on.
#  @param prefix_map Dictionary of prefixes : namespaces to set.
def set_prefixes(elem, prefix_map):
    if not ET.iselement(elem):
        elem = elem.getroot()

    for prefix, uri in prefix_map.items():
        elem.set("xmlns:" + prefix, uri)

## Returns a normalized version of the tag name.
#  @param name Tag name.
#
# This does two things: remove the way ElementTree handles XML
# namespaces, and put the text into lowercase.
def normalize(name):
    if name[0] == "{":
        uri, tag = name[1:].split("}")
        output = uri + ":" + tag
    else:
        output = name

    return output.lower()
