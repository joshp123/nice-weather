// Written by AI on 2026.10.16 by model GPT-6.1 Sol.
use std::alloc::{alloc, dealloc, Layout};

// Both encoded bytes and decoded f32 buffers use alignment four.
#[no_mangle]
pub unsafe extern "C" fn allocate(length: u32) -> u32 {
    match Layout::from_size_align(length as usize, 4) {
        Ok(layout) => alloc(layout) as u32,
        Err(_) => 0,
    }
}

#[no_mangle]
pub unsafe extern "C" fn deallocate(pointer: u32, length: u32) {
    if pointer != 0 {
        if let Ok(layout) = Layout::from_size_align(length as usize, 4) {
            dealloc(pointer as *mut u8, layout);
        }
    }
}

// Returns the number of f32 values, -1 for invalid Pco data or -2 when the
// caller's output capacity (in floats, not bytes) is too small.
#[no_mangle]
pub unsafe extern "C" fn decode(
    input_pointer: u32,
    input_length: u32,
    output_pointer: u32,
    output_capacity: u32,
) -> i32 {
    let input = std::slice::from_raw_parts(input_pointer as *const u8, input_length as usize);
    let output = std::slice::from_raw_parts_mut(output_pointer as *mut f32, output_capacity as usize);
    match pco::standalone::simple_decompress_into::<f32>(input, output) {
        Ok(progress) if progress.finished => progress.n_processed as i32,
        Ok(_) => -2,
        Err(_) => -1,
    }
}
