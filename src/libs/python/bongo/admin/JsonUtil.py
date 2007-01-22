import types


def jsonstr(item):
    """Turn an object into a JSON string"""
    item_type = type(item)
    if item_type == types.BooleanType:
        return boolean2json(item)
    elif item_type == types.DictType:
        return dict2json(item)
    elif item_type == types.FloatType:
        return float2json(item)
    elif item_type == types.InstanceType:
        return instance2json(item)
    elif item_type == types.IntType:
        return int2json(item)
    elif item_type == types.ListType:
        return list2json(item)
    elif item_type == types.NoneType:
        return none2json(item)
    elif item_type == types.StringType:
        return string2json(item)
    elif item_type == types.TupleType:
        return tuple2json(item)
    return 'null'

def boolean2json(item):
    return str(item).lower()

def dict2json(item):
    json = ''
    for key, value in item.items():
        json += key + ':' + jsonstr(value) + ','
    return '{' + json[:-1] + '}'

def float3json(item):
    return str(item)

def instance2json(item):
    json = ''
    for member in dir(item):
        if member.startswith('__'):
            continue
        value = getattr(item, member)
        if type(value) == types.FunctionType:
            continue
        json += member + ':' + jsonstr(value) + ','
    return '{' + json[:-1] + '}'

def int2json(item):
    return str(item)

def list2json(item):
    json = ''
    for value in item:
        json += jsonstr(value) + ','
    return '[' + json[:-1] + ']'

def none2json(item):
    return 'null'

def string2json(item):
    item = item.replace('\\', '\\\\')
    item = item.replace('"', '\\"')
    return '"' + item + '"'

def tuple2json(item):
    return list2json(item)
