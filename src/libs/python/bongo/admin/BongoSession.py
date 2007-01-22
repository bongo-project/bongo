import os
import time
import random
import pickle
import md5
from bongo.dragonfly import BongoCookie as Cookie

class BongoSession(dict):
    def __init__(self, req, lock=1):
        self.req = req

        cookie = Cookie.get_cookies(req).get('sid', None)
        if cookie is None:
            digest = md5.new(req.oldreq.address_string())
            digest.update(str(time.time()))
            self.sid = digest.hexdigest()

            # 'path' should be the application path. e.g. '/bongoadmin'
            Cookie.add_cookie(req, 'sid', self.sid, path='/', \
                              expires=time.time()+3600)
        else:
            self.sid = cookie.value

        # FIXME: find a better place to store sessions
        self.path = '/tmp/' + str(self.sid) + '.session'

    def save(self):
        f = open(self.path, 'w')
        pickle.dump(dict(self), f)
        f.close()

    def load(self):
        try:
            f = open(self.path)
            self.update(pickle.load(f))
            f.close()
        except IOError, e:
            pass

    def invalidate(self):
        os.remove(self.path)

try:
    from mod_python import Session as ApacheSession
    Session = ApacheSession.Session
except ImportError:
    Session = BongoSession
