## Helper functions for use with etree.

import lxml.etree as ET

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
