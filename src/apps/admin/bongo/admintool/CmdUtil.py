from bongo.admin import Util

def RecordAttributes(option, opt_str, value, parser):
    values = parser.values

    values.ensure_value("attributes", {})

    key = opt_str.lstrip("-")
    values.attributes.setdefault(key, []).append(value)

def GetAttributeArgs(*objClasses):
    attrs = []
    for c in objClasses:
        attrs.extend(Util.GetClassAttributes(c))

    args = []
    for attr in attrs:
        arg = Util.PropertyArguments[attr]
        args.append((arg, Util.PropertiesPrintable[attr]))
    args.sort()

    return args

def GetAgentArgs():
    from bongo.bootstrap import msgapi

    attrs = {}

    commonCache = {}

    common = GetAttributeArgs(msgapi.C_AGENT)

    for arg, desc in common:
        commonCache[arg] = True

    attrs[msgapi.C_AGENT] = common

    for agent in Util.Agents:
        agentAttrs = GetAttributeArgs(agent)

        for arg in agentAttrs:
            if not commonCache.has_key(arg[0]):
                attrs.setdefault(agent, []).append(arg)

    return attrs
