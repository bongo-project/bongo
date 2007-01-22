/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_debug_pool_
#define _lucene_debug_pool_
//this code is based on the boost memory pool library

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif
#ifdef LUCENE_ENABLE_MEMORY_POOL //no point including if not using mempool
CL_NS_DEF(debug)

void lucene_release_memory_pool();

//predefine some classes
template <typename UserAllocator=default_user_allocator_malloc_free>
class pool;
struct default_user_allocator_malloc_free;

///The LucenePoolBase
template <pool<>* mempool>
#ifdef LUCENE_ENABLE_REFCOUNT
   class LucenePoolBase:public LuceneBase{
#else
   class LucenePoolBase{
#endif
public:
   LucenePoolBase(){
   }
   static void* operator new (size_t size){
      return mempool->ordered_malloc();
   }
   static void operator delete (void *p){
      mempool->ordered_free(p);
   }
};


/////////////////////

//
// gcd is an algorithm that calculates the greatest common divisor of two
//  integers, using Euclid's algorithm.
//
// Pre: A > 0 && B > 0
// Recommended: A > B
template <typename Integer>
Integer gcd(Integer A, Integer B)
{
  do
  {
    const Integer tmp(B);
    B = A % B;
    A = tmp;
  } while (B != 0);

  return A;
}
//
// lcm is an algorithm that calculates the least common multiple of two
//  integers.
//
// Pre: A > 0 && B > 0
// Recommended: A > B
template <typename Integer>
Integer lcm(const Integer & A, const Integer & B)
{
  Integer ret = A;
  ret /= gcd(A, B);
  ret *= B;
  return ret;
}

struct default_user_allocator_malloc_free
{
  typedef std::size_t size_type;
  typedef std::ptrdiff_t difference_type;

  static char * malloc(const size_type bytes)
  { return reinterpret_cast<char *>(std::malloc(bytes)); }
  static void free(char * const block)
  { std::free(block); }
};

// PODptr is a class that pretends to be a "pointer" to different class types
//  that don't really exist.  It provides member functions to access the "data"
//  of the "object" it points to.  Since these "class" types are of variable
//  size, and contains some information at the *end* of its memory (for
//  alignment reasons), PODptr must contain the size of this "class" as well as
//  the pointer to this "object".
// PODptr is a class that pretends to be a "pointer" to different class types
//  that don't really exist.  It provides member functions to access the "data"
//  of the "object" it points to.  Since these "class" types are of variable
//  size, and contains some information at the *end* of its memory (for
//  alignment reasons), PODptr must contain the size of this "class" as well as
//  the pointer to this "object".
template <typename SizeType>
class PODptr
{
  public:
    typedef SizeType size_type;

  private:
    char * ptr;
    size_type sz;
    size_t lcm_size;

    char * ptr_next_size() const
    { return (ptr + sz - sizeof(size_type)); }
    char * ptr_next_ptr() const
    {
      return (ptr_next_size() - lcm_size);
    }

  public:
    PODptr(char * const nptr, const size_type nsize)
    :ptr(nptr), sz(nsize), lcm_size(lcm(sizeof(size_type), sizeof(void *))) { }
    PODptr()
    :ptr(0), sz(0), lcm_size(lcm(sizeof(size_type), sizeof(void *))) { }

    bool valid() const { return (begin() != 0); }
    void invalidate() { begin() = 0; }
    char * & begin() { return ptr; }
    char * begin() const { return ptr; }
    char * end() const { return ptr_next_ptr(); }
    size_type total_size() const { return sz; }
    size_type element_size() const
    {
      return (sz - sizeof(size_type) - lcm_size);
    }

    size_type & next_size() const
    { return *(reinterpret_cast<size_type *>(ptr_next_size())); }
    char * & next_ptr() const
    { return *(reinterpret_cast<char **>(ptr_next_ptr())); }

    PODptr next() const
    { return PODptr<size_type>(next_ptr(), next_size()); }
    void next(const PODptr & arg) const
    {
      next_ptr() = arg.begin();
      next_size() = arg.total_size();
    }
};

template <typename SizeType>
class simple_segregated_storage
{
  public:
    typedef SizeType size_type;

  private:
    simple_segregated_storage(const simple_segregated_storage &);
    void operator=(const simple_segregated_storage &);

    // pre: (n > 0), (start != 0), (nextof(start) != 0)
    // post: (start != 0)
    static void * try_malloc_n(void * & start, size_type n,
        size_type partition_size);

  protected:
    void * first;

    // Traverses the free list referred to by "first",
    //  and returns the iterator previous to where
    //  "ptr" would go if it was in the free list.
    // Returns 0 if "ptr" would go at the beginning
    //  of the free list (i.e., before "first")
    void * find_prev(void * ptr);

    // for the sake of code readability :)
    static void * & nextof(void * const ptr)
    { return *(static_cast<void **>(ptr)); }

  public:
    // Post: empty()
    simple_segregated_storage()
    :first(0) { }

    // pre: npartition_sz >= sizeof(void *)
    //      npartition_sz = sizeof(void *) * i, for some integer i
    //      nsz >= npartition_sz
    //      block is properly aligned for an array of object of
    //        size npartition_sz and array of void *
    // The requirements above guarantee that any pointer to a chunk
    //  (which is a pointer to an element in an array of npartition_sz)
    //  may be cast to void **.
    static void * segregate(void * block,
        size_type nsz, size_type npartition_sz,
        void * end = 0);

    // Same preconditions as 'segregate'
    // Post: !empty()
    void add_block(void * const block,
        const size_type nsz, const size_type npartition_sz)
    {
      // Segregate this block and merge its free list into the
      //  free list referred to by "first"
      first = segregate(block, nsz, npartition_sz, first);
    }

    // Same preconditions as 'segregate'
    // Post: !empty()
    void add_ordered_block(void * const block,
        const size_type nsz, const size_type npartition_sz)
    {
      // This (slower) version of add_block segregates the
      //  block and merges its free list into our free list
      //  in the proper order

      // Find where "block" would go in the free list
      void * const loc = find_prev(block);

      // Place either at beginning or in middle/end
      if (loc == 0)
        add_block(block, nsz, npartition_sz);
      else
        nextof(loc) = segregate(block, nsz, npartition_sz, nextof(loc));
    }

    // default destructor

    bool empty() const { return (first == 0); }

    // pre: !empty()
    void * malloc()
    {
      void * const ret = first;

      // Increment the "first" pointer to point to the next chunk
      first = nextof(first);
      return ret;
    }

    // pre: chunk was previously returned from a malloc() referring to the
    //  same free list
    // post: !empty()
    void free(void * const chunk)
    {
      nextof(chunk) = first;
      first = chunk;
    }

    // pre: chunk was previously returned from a malloc() referring to the
    //  same free list
    // post: !empty()
    void ordered_free(void * const chunk)
    {
      // This (slower) implementation of 'free' places the memory
      //  back in the list in its proper order.

      // Find where "chunk" goes in the free list
      void * const loc = find_prev(chunk);

      // Place either at beginning or in middle/end
      if (loc == 0)
        free(chunk);
      else
      {
        nextof(chunk) = nextof(loc);
        nextof(loc) = chunk;
      }
    }

    // Note: if you're allocating/deallocating n a lot, you should
    //  be using an ordered pool.
    void * malloc_n(size_type n, size_type partition_size);

    // pre: chunks was previously allocated from *this with the same
    //   values for n and partition_size
    // post: !empty()
    // Note: if you're allocating/deallocating n a lot, you should
    //  be using an ordered pool.
    void free_n(void * const chunks, const size_type n,
        const size_type partition_size)
    {
      add_block(chunks, n * partition_size, partition_size);
    }

    // pre: chunks was previously allocated from *this with the same
    //   values for n and partition_size
    // post: !empty()
    void ordered_free_n(void * const chunks, const size_type n,
        const size_type partition_size)
    {
      add_ordered_block(chunks, n * partition_size, partition_size);
    }
};


template <typename UserAllocator>
class pool: protected simple_segregated_storage<typename UserAllocator::size_type>
{
  public:
    typedef UserAllocator user_allocator;
    typedef typename UserAllocator::size_type size_type;
    typedef typename UserAllocator::difference_type difference_type;
  private:
    // Returns 0 if out-of-memory
    // Called if malloc/ordered_malloc needs to resize the free list
    void * malloc_need_resize();
    void * ordered_malloc_need_resize();

    const size_t min_alloc_size;
    const size_t min_alloc_size2;
  protected:
    PODptr<size_type> list;

    simple_segregated_storage<size_type> & store() { return *this; }
    const simple_segregated_storage<size_type> & store() const { return *this; }
    const size_type requested_size;
    size_type next_size;


    // is_from() tests a chunk to determine if it belongs in a block
    static bool is_from(void * const chunk, char * const i,
        const size_type sizeof_i)
    {
      std::less_equal<void *> lt_eq;
      std::less<void *> lt;
      return (lt_eq(i, chunk) && lt(chunk, i + sizeof_i));
    }

    size_type alloc_size() const
    {
      size_type min_size = min_alloc_size;
      return lcm<size_type>(requested_size, min_size);
    }

    // for the sake of code readability :)
    static void * & nextof(void * const ptr)
    { return *(static_cast<void **>(ptr)); }

  public:
    // The second parameter here is an extension!
    // pre: npartition_size != 0 && nnext_size != 0
    explicit pool(const size_type nrequested_size,
        const size_type nnext_size = 32)
    :requested_size(nrequested_size), next_size(nnext_size),
    min_alloc_size(lcm(sizeof(void *), sizeof(size_type))),
    min_alloc_size2(lcm(sizeof(size_type), sizeof(void *)))
    { }

    size_type total_size() const { return list.total_size(); }
    ~pool() { purge_memory(); }

    // Releases memory blocks that don't have chunks allocated
    // pre: lists are ordered
    //  Returns true if memory was actually deallocated
    bool release_memory();

    // Releases *all* memory blocks, even if chunks are still allocated
    //  Returns true if memory was actually deallocated
    bool purge_memory();

    // These functions are extensions!
    size_type get_next_size() const { return next_size; }
    void set_next_size(const size_type nnext_size) { next_size = nnext_size; }

    // Both malloc and ordered_malloc do a quick inlined check first for any
    //  free chunks.  Only if we need to get another memory block do we call
    //  the non-inlined *_need_resize() functions.
    // Returns 0 if out-of-memory
    void * malloc()
    {
      // Look for a non-empty storage
      if (!store().empty())
        return store().malloc();
      return malloc_need_resize();
    }

    void * ordered_malloc()
    {
      // Look for a non-empty storage
      if (!store().empty())
        return store().malloc();
      return ordered_malloc_need_resize();
    }

    // Returns 0 if out-of-memory
    // Allocate a contiguous section of n chunks
    void * ordered_malloc(size_type n);

    // pre: 'chunk' must have been previously
    //        returned by *this.malloc().
    void free(void * const chunk)
    { store().free(chunk); }

    // pre: 'chunk' must have been previously
    //        returned by *this.malloc().
    void ordered_free(void * const chunk)
    { store().ordered_free(chunk); }

    // pre: 'chunk' must have been previously
    //        returned by *this.malloc(n).
    void ordered_free(void * const chunks, const size_type n)
    {
      const size_type partition_size = alloc_size();
      const size_type total_req_size = n * requested_size;
      const size_type num_chunks = total_req_size / partition_size +
          ((total_req_size % partition_size)!=0);

      store().ordered_free_n(chunks, num_chunks, partition_size);
    }

    // pre: 'chunk' must have been previously
    //        returned by *this.malloc(n).
    void free(void * const chunks, const size_type n)
    {
      const size_type partition_size = alloc_size();
      const size_type total_req_size = n * requested_size;
      const size_type num_chunks = total_req_size / partition_size +
          static_cast<bool>( (total_req_size % partition_size) != 0 );

      store().free_n(chunks, num_chunks, partition_size);
    }

};




template <typename UserAllocator>
bool pool<UserAllocator>::purge_memory()
{
  PODptr<size_type> iter = list;

  if (!iter.valid())
    return false;

  do
  {
    // hold "next" pointer
    const PODptr<size_type> next = iter.next();

    // delete the storage
    UserAllocator::free(iter.begin());

    // increment iter
    iter = next;
  } while (iter.valid());

  list.invalidate();
  this->first = 0;

  return true;
}
template <typename UserAllocator>
bool pool<UserAllocator>::release_memory()
{
  // This is the return value: it will be set to true when we actually call
  //  UserAllocator::free(..)
  bool ret = false;

  // This is a current & previous iterator pair over the memory block list
  PODptr<size_type> ptr = list;
  PODptr<size_type> prev;

  // This is a current & previous iterator pair over the free memory chunk list
  //  Note that "prev_free" in this case does NOT point to the previous memory
  //  chunk in the free list, but rather the last free memory chunk before the
  //  current block.
  void * free = this->first;
  void * prev_free = 0;

  const size_type partition_size = alloc_size();

  // Search through all the all the allocated memory blocks
  while (ptr.valid())
  {
    // At this point:
    //  ptr points to a valid memory block
    //  free points to either:
    //    0 if there are no more free chunks
    //    the first free chunk in this or some next memory block
    //  prev_free points to either:
    //    the last free chunk in some previous memory block
    //    0 if there is no such free chunk

    // If there are no more free memory chunks, then every remaining
    //  block is allocated out to its fullest capacity, and we can't
    //  release any more memory
    if (free == 0)
      return ret;

    // We have to check all the chunks.  If they are *all* free (i.e., present
    //  in the free list), then we can free the block.
    bool all_chunks_free = true;

    // Iterate 'i' through all chunks in the memory block
    for (char * i = ptr.begin(); i != ptr.end(); i += partition_size)
    {
      // If this chunk is not free
      if (i != free)
      {
        // We won't be able to free this block
        all_chunks_free = false;

        // Abort searching the chunks; we won't be able to free this
        //  block because a chunk is not free.
        break;
      }

      // We do not increment prev_free because we are in the same block
      free = nextof(free);
    }

    const PODptr<size_type> next = ptr.next();

    if (!all_chunks_free)
    {
      // Rush through all free chunks from this block
      std::less<void *> lt;
      void * const last = ptr.end() - partition_size;
      do
      {
        free = nextof(free);
      } while (lt(free, last));

      // Increment free one more time and set prev_free to maintain the
      //  invariants:
      //     free points to the first free chunk in some next memory block, or
      //       0 if there is no such chunk.
      //     prev_free points to the last free chunk in this memory block.
      prev_free = free;
      free = nextof(free);
    }
    else
    {
      // All chunks from this block are free

      // Remove block from list
      if (prev.valid())
        prev.next(next);
      else
        list = next;

      // Remove all entries in the free list from this block
      if (prev_free != 0)
        nextof(prev_free) = free;
      else
        this->first = free;

      // And release memory
      UserAllocator::free(ptr.begin());
      ret = true;
    }

    // Increment ptr
    ptr = next;
  }

  return ret;
}
template <typename UserAllocator>
void * pool<UserAllocator>::ordered_malloc(const size_type n)
{
  const size_type partition_size = alloc_size();
  const size_type total_req_size = n * requested_size;
  const size_type num_chunks = total_req_size / partition_size +
      ((total_req_size % partition_size)!=0 );

  void * ret = store().malloc_n(num_chunks, partition_size);

  if (ret != 0)
    return ret;

  // Not enougn memory in our storages; make a new storage,
  next_size = max(next_size, num_chunks);
  const size_type POD_size = next_size * partition_size +
      min_alloc_size2 + sizeof(size_type);
  char* const ptr = UserAllocator::malloc(POD_size);
  if (ptr == 0)
    return 0;
  const PODptr<size_type> node(ptr, POD_size);

  // Split up block so we can use what wasn't requested
  //  (we can use "add_block" here because we know that
  //  the free list is empty, so we don't have to use
  //  the slower ordered version)
  if (next_size > num_chunks)
    store().add_block(node.begin() + num_chunks * partition_size,
        node.element_size() - num_chunks * partition_size, partition_size);

  next_size <<= 1;

  //  insert it into the list,
  //   handle border case
  if (!list.valid() || std::greater<void *>()(list.begin(), node.begin()))
  {
    node.next(list);
    list = node;
  }
  else
  {
    PODptr<size_type> prev = list;

    while (true)
    {
      // if we're about to hit the end or
      //  if we've found where "node" goes
      if (prev.next_ptr() == 0
          || std::greater<void *>()(prev.next_ptr(), node.begin()))
        break;

      prev = prev.next();
    }

    node.next(prev.next());
    prev.next(node);
  }

  //  and return it.
  return node.begin();
}

template <typename SizeType>
void * simple_segregated_storage<SizeType>::malloc_n(const size_type n,
    const size_type partition_size)
{
  void * start = &first;
  void * iter;
  do
  {
    if (nextof(start) == 0)
      return 0;
    iter = try_malloc_n(start, n, partition_size);
  } while (iter == 0);
  void * const ret = nextof(start);
  nextof(start) = nextof(iter);
  return ret;
}

template <typename SizeType>
void * simple_segregated_storage<SizeType>::find_prev(void * const ptr)
{
  // Handle border case
  if (first == 0 || std::greater<void *>()(first, ptr))
    return 0;

  void * iter = first;
  while (true)
  {
    // if we're about to hit the end or
    //  if we've found where "ptr" goes
    if (nextof(iter) == 0 || std::greater<void *>()(nextof(iter), ptr))
      return iter;

    iter = nextof(iter);
  }
}


template <typename SizeType>
void * simple_segregated_storage<SizeType>::segregate(
    void * const block,
    const size_type sz,
    const size_type partition_sz,
    void * const end)
{
  // Get pointer to last valid chunk, preventing overflow on size calculations
  //  The division followed by the multiplication just makes sure that
  //    old == block + partition_sz * i, for some integer i, even if the
  //    block size (sz) is not a multiple of the partition size.
  char * old = static_cast<char *>(block)
      + ((sz - partition_sz) / partition_sz) * partition_sz;

  // Set it to point to the end
  nextof(old) = end;

  // Handle border case where sz == partition_sz (i.e., we're handling an array
  //  of 1 element)
  if (old == block)
    return block;

  // Iterate backwards, building a singly-linked list of pointers
  for (char * iter = old - partition_sz; iter != block;
      old = iter, iter -= partition_sz)
    nextof(iter) = old;

  // Point the first pointer, too
  nextof(block) = old;

  return block;
}

// The following function attempts to find n contiguous chunks
//  of size partition_size in the free list, starting at start.
// If it succeds, it returns the last chunk in that contiguous
//  sequence, so that the sequence is known by [start, {retval}]
// If it fails, it does do either because it's at the end of the
//  free list or hits a non-contiguous chunk.  In either case,
//  it will return 0, and set start to the last considered
//  chunk.  You are at the end of the free list if
//  nextof(start) == 0.  Otherwise, start points to the last
//  chunk in the contiguous sequence, and nextof(start) points
//  to the first chunk in the next contiguous sequence (assuming
//  an ordered free list)
template <typename SizeType>
void * simple_segregated_storage<SizeType>::try_malloc_n(
    void * & start, size_type n, const size_type partition_size)
{
  void * iter = nextof(start);
  while (--n != 0)
  {
    void * next = nextof(iter);
    if (next != static_cast<char *>(iter) + partition_size)
    {
      // next == 0 (end-of-list) or non-contiguous chunk found
      start = iter;
      return 0;
    }
    iter = next;
  }
  return iter;
}
template <typename UserAllocator>
void * pool<UserAllocator>::ordered_malloc_need_resize()
{
  // No memory in any of our storages; make a new storage,
  const size_type partition_size = alloc_size();
  const size_type POD_size = next_size * partition_size +
      min_alloc_size2 + sizeof(size_type);
  char * const ptr = UserAllocator::malloc(POD_size);
  if (ptr == 0)
    return 0;
  const PODptr<size_type> node(ptr, POD_size);
  next_size <<= 1;

  //  initialize it,
  //  (we can use "add_block" here because we know that
  //  the free list is empty, so we don't have to use
  //  the slower ordered version)
  store().add_block(node.begin(), node.element_size(), partition_size);

  //  insert it into the list,
  //   handle border case
  if (!list.valid() || std::greater<void *>()(list.begin(), node.begin()))
  {
    node.next(list);
    list = node;
  }
  else
  {
    PODptr<size_type> prev = list;

    while (true)
    {
      // if we're about to hit the end or
      //  if we've found where "node" goes
      if (prev.next_ptr() == 0
          || std::greater<void *>()(prev.next_ptr(), node.begin()))
        break;

      prev = prev.next();
    }

    node.next(prev.next());
    prev.next(node);
  }

  //  and return a chunk from it.
  return store().malloc();
}
CL_NS_END

#endif //LUCENE_ENABLE_MEMORY_POOL
#endif  //lucene_debug_pool
