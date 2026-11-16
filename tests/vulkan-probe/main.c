// Read-only GPU smoke test for an isolated Android guest. It submits a Vulkan
// image clear and verifies the result after a GPU-to-host copy.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <vulkan/vulkan.h>

#define CHECK(x) do { VkResult rc = (x); if (rc != VK_SUCCESS) { fprintf(stderr, "%s failed: %d\n", #x, rc); return 1; } } while (0)

static uint32_t memory_type(VkPhysicalDevice p, uint32_t bits, VkMemoryPropertyFlags flags) {
    VkPhysicalDeviceMemoryProperties props;
    vkGetPhysicalDeviceMemoryProperties(p, &props);
    for (uint32_t i = 0; i < props.memoryTypeCount; ++i)
        if ((bits & (1u << i)) && (props.memoryTypes[i].propertyFlags & flags) == flags) return i;
    return UINT32_MAX;
}

int main(void) {
    VkApplicationInfo ai = { .sType = VK_STRUCTURE_TYPE_APPLICATION_INFO, .pApplicationName = "NekoVulkanProbe", .apiVersion = VK_API_VERSION_1_0 };
    VkInstanceCreateInfo ici = { .sType = VK_STRUCTURE_TYPE_INSTANCE_CREATE_INFO, .pApplicationInfo = &ai };
    VkInstance instance; CHECK(vkCreateInstance(&ici, NULL, &instance));
    uint32_t count = 0; CHECK(vkEnumeratePhysicalDevices(instance, &count, NULL));
    if (!count) { fprintf(stderr, "no Vulkan device\n"); return 1; }
    VkPhysicalDevice *devices = calloc(count, sizeof(*devices)); if (!devices) return 1;
    CHECK(vkEnumeratePhysicalDevices(instance, &count, devices));
    VkPhysicalDevice physical = VK_NULL_HANDLE; VkPhysicalDeviceProperties props;
    for (uint32_t i = 0; i < count; ++i) {
        vkGetPhysicalDeviceProperties(devices[i], &props);
        if (props.deviceType == VK_PHYSICAL_DEVICE_TYPE_DISCRETE_GPU || props.deviceType == VK_PHYSICAL_DEVICE_TYPE_INTEGRATED_GPU) { physical = devices[i]; break; }
    }
    free(devices); if (physical == VK_NULL_HANDLE) { fprintf(stderr, "no hardware Vulkan device\n"); return 1; }
    uint32_t queues = 0; vkGetPhysicalDeviceQueueFamilyProperties(physical, &queues, NULL);
    VkQueueFamilyProperties *qp = calloc(queues, sizeof(*qp)); if (!qp) return 1;
    vkGetPhysicalDeviceQueueFamilyProperties(physical, &queues, qp);
    uint32_t family = UINT32_MAX;
    for (uint32_t i = 0; i < queues; ++i) if (qp[i].queueCount && (qp[i].queueFlags & VK_QUEUE_GRAPHICS_BIT)) { family = i; break; }
    free(qp); if (family == UINT32_MAX) { fprintf(stderr, "no graphics queue\n"); return 1; }
    float priority = 1.0f;
    VkDeviceQueueCreateInfo qci = { .sType = VK_STRUCTURE_TYPE_DEVICE_QUEUE_CREATE_INFO, .queueFamilyIndex = family, .queueCount = 1, .pQueuePriorities = &priority };
    VkDeviceCreateInfo dci = { .sType = VK_STRUCTURE_TYPE_DEVICE_CREATE_INFO, .queueCreateInfoCount = 1, .pQueueCreateInfos = &qci };
    VkDevice device; CHECK(vkCreateDevice(physical, &dci, NULL, &device)); VkQueue queue; vkGetDeviceQueue(device, family, 0, &queue);
    VkBufferCreateInfo bci = { .sType = VK_STRUCTURE_TYPE_BUFFER_CREATE_INFO, .size = 16*16*4, .usage = VK_BUFFER_USAGE_TRANSFER_DST_BIT, .sharingMode = VK_SHARING_MODE_EXCLUSIVE };
    VkBuffer buffer; CHECK(vkCreateBuffer(device, &bci, NULL, &buffer)); VkMemoryRequirements br; vkGetBufferMemoryRequirements(device, buffer, &br);
    uint32_t host_type = memory_type(physical, br.memoryTypeBits, VK_MEMORY_PROPERTY_HOST_VISIBLE_BIT | VK_MEMORY_PROPERTY_HOST_COHERENT_BIT);
    if (host_type == UINT32_MAX) { fprintf(stderr, "no coherent host-visible memory\n"); return 1; }
    VkMemoryAllocateInfo ba = { .sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO, .allocationSize = br.size, .memoryTypeIndex = host_type };
    VkDeviceMemory buffer_memory; CHECK(vkAllocateMemory(device, &ba, NULL, &buffer_memory)); CHECK(vkBindBufferMemory(device, buffer, buffer_memory, 0));
    VkImageCreateInfo ii = { .sType = VK_STRUCTURE_TYPE_IMAGE_CREATE_INFO, .imageType = VK_IMAGE_TYPE_2D, .format = VK_FORMAT_R8G8B8A8_UNORM, .extent = {16,16,1}, .mipLevels = 1, .arrayLayers = 1, .samples = VK_SAMPLE_COUNT_1_BIT, .tiling = VK_IMAGE_TILING_OPTIMAL, .usage = VK_IMAGE_USAGE_TRANSFER_SRC_BIT | VK_IMAGE_USAGE_TRANSFER_DST_BIT, .sharingMode = VK_SHARING_MODE_EXCLUSIVE, .initialLayout = VK_IMAGE_LAYOUT_UNDEFINED };
    VkImage img; CHECK(vkCreateImage(device, &ii, NULL, &img)); VkMemoryRequirements ir; vkGetImageMemoryRequirements(device, img, &ir);
    uint32_t image_type = memory_type(physical, ir.memoryTypeBits, VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT);
    if (image_type == UINT32_MAX) image_type = memory_type(physical, ir.memoryTypeBits, 0);
    if (image_type == UINT32_MAX) { fprintf(stderr, "no image memory\n"); return 1; }
    VkMemoryAllocateInfo ia = { .sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO, .allocationSize = ir.size, .memoryTypeIndex = image_type };
    VkDeviceMemory image_memory; CHECK(vkAllocateMemory(device, &ia, NULL, &image_memory)); CHECK(vkBindImageMemory(device, img, image_memory, 0));
    VkCommandPoolCreateInfo pci = { .sType = VK_STRUCTURE_TYPE_COMMAND_POOL_CREATE_INFO, .queueFamilyIndex = family };
    VkCommandPool pool; CHECK(vkCreateCommandPool(device, &pci, NULL, &pool));
    VkCommandBufferAllocateInfo cai = { .sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_ALLOCATE_INFO, .commandPool = pool, .level = VK_COMMAND_BUFFER_LEVEL_PRIMARY, .commandBufferCount = 1 };
    VkCommandBuffer cmd; CHECK(vkAllocateCommandBuffers(device, &cai, &cmd)); VkCommandBufferBeginInfo begin = { .sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO };
    CHECK(vkBeginCommandBuffer(cmd, &begin));
    VkImageSubresourceRange range = { .aspectMask = VK_IMAGE_ASPECT_COLOR_BIT, .levelCount = 1, .layerCount = 1 };
    VkImageMemoryBarrier bar = { .sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER, .srcAccessMask = 0, .dstAccessMask = VK_ACCESS_TRANSFER_WRITE_BIT, .oldLayout = VK_IMAGE_LAYOUT_UNDEFINED, .newLayout = VK_IMAGE_LAYOUT_TRANSFER_DST_OPTIMAL, .srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED, .dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED, .image = img, .subresourceRange = range };
    vkCmdPipelineBarrier(cmd, VK_PIPELINE_STAGE_TOP_OF_PIPE_BIT, VK_PIPELINE_STAGE_TRANSFER_BIT, 0, 0, NULL, 0, NULL, 1, &bar);
    VkClearColorValue color = { .float32 = {1.0f, 0.25f, 0.5f, 1.0f} }; vkCmdClearColorImage(cmd, img, VK_IMAGE_LAYOUT_TRANSFER_DST_OPTIMAL, &color, 1, &range);
    bar.srcAccessMask = VK_ACCESS_TRANSFER_WRITE_BIT; bar.dstAccessMask = VK_ACCESS_TRANSFER_READ_BIT; bar.oldLayout = VK_IMAGE_LAYOUT_TRANSFER_DST_OPTIMAL; bar.newLayout = VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL;
    vkCmdPipelineBarrier(cmd, VK_PIPELINE_STAGE_TRANSFER_BIT, VK_PIPELINE_STAGE_TRANSFER_BIT, 0, 0, NULL, 0, NULL, 1, &bar);
    VkBufferImageCopy copy = { .imageSubresource = { .aspectMask = VK_IMAGE_ASPECT_COLOR_BIT, .layerCount = 1 }, .imageExtent = {16,16,1} };
    vkCmdCopyImageToBuffer(cmd, img, VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL, buffer, 1, &copy);
    VkBufferMemoryBarrier bb = { .sType = VK_STRUCTURE_TYPE_BUFFER_MEMORY_BARRIER, .srcAccessMask = VK_ACCESS_TRANSFER_WRITE_BIT, .dstAccessMask = VK_ACCESS_HOST_READ_BIT, .srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED, .dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED, .buffer = buffer, .size = VK_WHOLE_SIZE };
    vkCmdPipelineBarrier(cmd, VK_PIPELINE_STAGE_TRANSFER_BIT, VK_PIPELINE_STAGE_HOST_BIT, 0, 0, NULL, 1, &bb, 0, NULL); CHECK(vkEndCommandBuffer(cmd));
    VkSubmitInfo submit = { .sType = VK_STRUCTURE_TYPE_SUBMIT_INFO, .commandBufferCount = 1, .pCommandBuffers = &cmd }; CHECK(vkQueueSubmit(queue, 1, &submit, VK_NULL_HANDLE)); CHECK(vkQueueWaitIdle(queue));
    uint8_t *pixels = NULL; CHECK(vkMapMemory(device, buffer_memory, 0, VK_WHOLE_SIZE, 0, (void **)&pixels));
    for (int i = 0; i < 16*16; ++i) if (pixels[i*4] != 255 || pixels[i*4+1] < 63 || pixels[i*4+1] > 65 || pixels[i*4+2] < 127 || pixels[i*4+2] > 129 || pixels[i*4+3] != 255) { fprintf(stderr, "GPU readback mismatch at pixel %d\n", i); return 1; }
    vkUnmapMemory(device, buffer_memory); printf("Vulkan image clear and readback passed on %s\n", props.deviceName);
    vkDestroyCommandPool(device, pool, NULL); vkDestroyImage(device, img, NULL); vkFreeMemory(device, image_memory, NULL); vkDestroyBuffer(device, buffer, NULL); vkFreeMemory(device, buffer_memory, NULL); vkDestroyDevice(device, NULL); vkDestroyInstance(instance, NULL);
    return 0;
}
