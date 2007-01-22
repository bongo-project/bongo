# This file is imported by the Hawkeye header.psp file, so all its
# functions can be used by any templates.

import urllib

def BuildAgentUrl(server, agent):
    return "/".join(("/admin/agents/edit",
                     urllib.quote(server), urllib.quote(agent)))

def BuildUserUrl(context, user):
    return "/".join(("/admin/users/edit",
                     urllib.quote(context), urllib.quote(user)))

def SplitQueryArgs(query):
    return [ urllib.unquote(s) for s in query.split("/") ]
