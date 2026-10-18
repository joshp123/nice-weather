"""A bounded synchronous HTTP range reader for anonymous HDF5 metadata.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
The async reader stalled on real B-tree reads. Keep its small-block cache,
but use explicit HTTP deadlines and retries so resumable acquisition advances.
"""
from collections import OrderedDict
import http.client
import io
import time
import urllib.request


class AnonymousRangeFile(io.RawIOBase):
    def __init__(self,url,block_size=65536,max_blocks=16384):
        self.url=url
        request=urllib.request.Request(url,method='HEAD')
        with urllib.request.urlopen(request,timeout=30) as response:
            self.size=int(response.headers['Content-Length'])
        self.position=0
        self.block_size=block_size
        self.max_blocks=max_blocks
        self.blocks=OrderedDict()
        self.requests=0
        self.transferred=0

    def readable(self):
        return True

    def seekable(self):
        return True

    def tell(self):
        return self.position

    def seek(self,offset,whence=0):
        self.position=offset if whence==0 else self.position+offset if whence==1 else self.size+offset
        return self.position

    def block(self,index):
        if index in self.blocks:
            self.blocks.move_to_end(index)
            return self.blocks[index]
        start=index*self.block_size
        size=min(self.block_size,self.size-start)
        for attempt in range(4):
            try:
                request=urllib.request.Request(self.url,headers={'Range':f'bytes={start}-{start+size-1}'})
                with urllib.request.urlopen(request,timeout=30) as response:
                    data=response.read()
                if len(data)!=size:
                    raise OSError(f'Truncated metadata range: {len(data)} != {size}')
                break
            except (OSError,TimeoutError,http.client.IncompleteRead):
                if attempt==3:
                    raise
                time.sleep(2**attempt)
        self.requests+=1
        self.transferred+=len(data)
        self.blocks[index]=data
        if len(self.blocks)>self.max_blocks:
            self.blocks.popitem(last=False)
        return data

    def read(self,size=-1):
        size=min(self.size-self.position,size if size>=0 else self.size-self.position)
        stop=self.position+size
        pieces=[]
        while self.position<stop:
            index,remainder=divmod(self.position,self.block_size)
            block=self.block(index)
            count=min(len(block)-remainder,stop-self.position)
            pieces.append(block[remainder:remainder+count])
            self.position+=count
        return b''.join(pieces)

    def readinto(self,target):
        data=self.read(len(target))
        target[:len(data)]=data
        return len(data)
